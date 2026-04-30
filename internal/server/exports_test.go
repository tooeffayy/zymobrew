package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

// setupExports returns a server backed by a real DB plus separate in-memory
// stores for user exports and admin backups (matching the production split),
// with the relevant tables pre-truncated for a clean slate. The returned
// MemStore is the export store — admin tests that need the backup store
// reach for setupExportsBoth instead.
func setupExports(t *testing.T, mode config.InstanceMode) (*server.Server, *testutil.MemStore) {
	t.Helper()
	srv, exportStore, _ := setupExportsBoth(t, mode)
	return srv, exportStore
}

func setupExportsBoth(t *testing.T, mode config.InstanceMode) (*server.Server, *testutil.MemStore, *testutil.MemStore) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	if _, err := pool.Exec(ctx,
		"TRUNCATE users, sessions, user_exports, admin_backups CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	exportStore := testutil.NewMemStore()
	backupStore := testutil.NewMemStore()
	return server.New(pool, config.Config{InstanceMode: mode}, exportStore, backupStore), exportStore, backupStore
}

// --- User export handler tests ---

func TestExports_TriggerAndList(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "exporter")

	// Trigger export → 202 with pending status.
	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", nil, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: want 202, got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	exportID, _ := body["id"].(string)
	if exportID == "" {
		t.Fatal("missing id in trigger response")
	}
	if body["status"] != "pending" {
		t.Fatalf("status = %v, want pending", body["status"])
	}

	// Duplicate trigger while one is pending → 409.
	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/exports", nil, cookies...)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate trigger: want 409, got %d", resp.StatusCode)
	}

	// List → one export.
	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	var list []any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// Get by ID → pending status.
	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID, nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: want 200, got %d", resp.StatusCode)
	}
	got := decodeMap(t, resp)
	if got["status"] != "pending" {
		t.Fatalf("get status = %v, want pending", got["status"])
	}
}

func TestExports_GetNotFound(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "exporter2")

	fakeID := "00000000-0000-0000-0000-000000000000"
	resp := doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+fakeID, nil, cookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestExports_DownloadNotCompleted(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "exporter3")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", nil, cookies...)
	body := decodeMap(t, resp)
	exportID, _ := body["id"].(string)

	// Download before the worker marks it complete → 409.
	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID+"/download", nil, cookies...)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("download pending: want 409, got %d", resp.StatusCode)
	}
}

func TestExports_TriggerWithFormat(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "fmt_user")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", map[string]any{"format": "tar.gz"}, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger tar.gz: want 202, got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["format"] != "tar.gz" {
		t.Fatalf("format = %v, want tar.gz", body["format"])
	}
}

func TestExports_TriggerRejectsBadFormat(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "bad_fmt_user")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", map[string]any{"format": "rar"}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad format: want 400, got %d", resp.StatusCode)
	}
}

func TestExports_TriggerEmptyBodyDefaultsToZip(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "default_fmt_user")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", nil, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("empty body: want 202, got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["format"] != "zip" {
		t.Fatalf("format = %v, want zip", body["format"])
	}
}

// TestExports_DownloadFormatHeaders asserts the format column drives both
// Content-Type and filename extension on the download response. Catches a
// regression where the handler hardcodes zip even when the export was
// completed as tar.gz or zstd.
func TestExports_DownloadFormatHeaders(t *testing.T) {
	srv, store := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "dl_fmt_user")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", map[string]any{"format": "tar.gz"}, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: want 202, got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	exportID, _ := body["id"].(string)

	// Mark the row complete by hand and seed the store with a stub blob —
	// we're testing the handler, not the worker.
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	stubKey := "tmp/exports/seed/" + exportID + ".tar.gz"
	if err := store.Put(ctx, stubKey, strings.NewReader("stub-archive-bytes"), int64(len("stub-archive-bytes"))); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE user_exports
		    SET status = 'complete', file_path = $1, size_bytes = 18, sha256 = $2, completed_at = now(), expires_at = now() + interval '1 day'
		  WHERE id = $3`,
		stubKey, strings.Repeat("a", 64), exportID,
	); err != nil {
		t.Fatalf("update export: %v", err)
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID+"/download", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasSuffix(got, `.tar.gz"`) {
		t.Errorf("Content-Disposition = %q, want suffix .tar.gz\"", got)
	}
}

// TestExports_DownloadCleansUpAfterSuccess verifies the export row is
// flipped to 'expired' and the underlying file deleted after a successful
// download — exports are single-use, the per-row TTL is just a safety net.
func TestExports_DownloadCleansUpAfterSuccess(t *testing.T) {
	srv, store := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "dl_cleanup_user")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/exports", nil, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: want 202, got %d", resp.StatusCode)
	}
	exportID, _ := decodeMap(t, resp)["id"].(string)

	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	stubKey := "tmp/exports/seed/" + exportID + ".zip"
	const stubBody = "stub-archive-bytes"
	if err := store.Put(ctx, stubKey, strings.NewReader(stubBody), int64(len(stubBody))); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE user_exports
		    SET status = 'complete', file_path = $1, size_bytes = $2, sha256 = $3, completed_at = now(), expires_at = now() + interval '1 hour'
		  WHERE id = $4`,
		stubKey, len(stubBody), strings.Repeat("a", 64), exportID,
	); err != nil {
		t.Fatalf("update export: %v", err)
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID+"/download", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: want 200, got %d", resp.StatusCode)
	}
	// Drain the body so the handler completes its post-stream cleanup before
	// we assert side effects.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("drain body: %v", err)
	}
	resp.Body.Close()

	if store.Has(stubKey) {
		t.Errorf("export blob still present at %q after successful download", stubKey)
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID, nil, cookies...)
	got := decodeMap(t, resp)
	if got["status"] != "expired" {
		t.Errorf("status after download = %v, want expired", got["status"])
	}

	// A second download attempt should now 409, not stream a missing file.
	resp = doJSON(t, srv, http.MethodGet, "/api/users/me/exports/"+exportID+"/download", nil, cookies...)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second download: want 409, got %d", resp.StatusCode)
	}
}

func TestExports_RequiresAuth(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)

	paths := []struct{ method, path string }{
		{http.MethodPost, "/api/users/me/exports"},
		{http.MethodGet, "/api/users/me/exports"},
	}
	for _, ep := range paths {
		resp := doJSON(t, srv, ep.method, ep.path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

// --- Admin backup handler tests ---

func TestAdminBackup_RequiresAdmin(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "regular_user")

	// Non-admin gets 403 on all admin endpoints.
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/admin/backups"},
		{http.MethodPost, "/api/admin/backups"},
		{http.MethodGet, "/api/admin/backups/00000000-0000-0000-0000-000000000000"},
	}
	for _, ep := range paths {
		resp := doJSON(t, srv, ep.method, ep.path, nil, cookies...)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: want 403, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

func TestAdminBackup_RequiresAuth(t *testing.T) {
	srv, _ := setupExports(t, config.ModeOpen)

	resp := doJSON(t, srv, http.MethodPost, "/api/admin/backups", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: want 401, got %d", resp.StatusCode)
	}
}

func TestAdminBackup_TriggerAndList(t *testing.T) {
	// single_user mode: first registered user becomes admin.
	srv, _ := setupExports(t, config.ModeSingleUser)
	cookies := registerHelper(t, srv, "admin_user")

	// Trigger backup → 202 with pending status.
	resp := doJSON(t, srv, http.MethodPost, "/api/admin/backups", nil, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: want 202, got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	backupID, _ := body["id"].(string)
	if backupID == "" {
		t.Fatal("missing id in trigger response")
	}
	if body["status"] != "pending" {
		t.Fatalf("status = %v, want pending", body["status"])
	}
	if body["storage_backend"] != "mem" {
		t.Fatalf("storage_backend = %v, want mem", body["storage_backend"])
	}

	// List → one backup.
	resp = doJSON(t, srv, http.MethodGet, "/api/admin/backups", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	var list []any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// Get by ID.
	resp = doJSON(t, srv, http.MethodGet, "/api/admin/backups/"+backupID, nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: want 200, got %d", resp.StatusCode)
	}
	got := decodeMap(t, resp)
	if got["status"] != "pending" {
		t.Fatalf("get status = %v, want pending", got["status"])
	}
}

func TestAdminBackup_GetNotFound(t *testing.T) {
	srv, _ := setupExports(t, config.ModeSingleUser)
	cookies := registerHelper(t, srv, "admin_user2")

	fakeID := "00000000-0000-0000-0000-000000000000"
	resp := doJSON(t, srv, http.MethodGet, "/api/admin/backups/"+fakeID, nil, cookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestAdminBackup_DownloadNotCompleted(t *testing.T) {
	srv, _ := setupExports(t, config.ModeSingleUser)
	cookies := registerHelper(t, srv, "admin_user3")

	resp := doJSON(t, srv, http.MethodPost, "/api/admin/backups", nil, cookies...)
	body := decodeMap(t, resp)
	backupID, _ := body["id"].(string)

	// Download before worker completes → 409.
	resp = doJSON(t, srv, http.MethodGet, "/api/admin/backups/"+backupID+"/download", nil, cookies...)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("download pending: want 409, got %d", resp.StatusCode)
	}
}
