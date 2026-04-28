package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

// setupExports returns a server backed by a real DB and an in-memory store,
// with the relevant tables pre-truncated for a clean slate.
func setupExports(t *testing.T, mode config.InstanceMode) (*server.Server, *testutil.MemStore) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	if _, err := pool.Exec(ctx,
		"TRUNCATE users, sessions, user_exports, admin_backups CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	store := testutil.NewMemStore()
	return server.New(pool, config.Config{InstanceMode: mode}, store), store
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
