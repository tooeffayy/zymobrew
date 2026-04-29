package jobs

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
	"zymobrew/internal/testutil"
)

// TestUserExportDispatchWorker seeds a pending user_export, runs the worker
// directly (no River runtime), and verifies the export reaches `complete`
// status with a valid ZIP written to the store.
func TestUserExportDispatchWorker(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	store := testutil.NewMemStore()

	// Run everything in a transaction that rolls back — no persistent state.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	q := queries.New(pool).WithTx(tx)

	user, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username: "export_worker_test",
		Email:    "export_worker_test@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	export, err := q.CreateUserExport(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if export.Status != queries.JobStatusPending {
		t.Fatalf("initial status = %s, want pending", export.Status)
	}

	worker := &userExportDispatchWorker{queries: q, store: store}
	if err := worker.Work(ctx, &river.Job[UserExportDispatchArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	updated, err := q.GetUserExport(ctx, queries.GetUserExportParams{
		ID:     export.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != queries.JobStatusComplete {
		t.Fatalf("status = %s, want complete (error: %s)", updated.Status, updated.Error.String)
	}
	if !updated.FilePath.Valid || updated.FilePath.String == "" {
		t.Fatal("file_path not set")
	}
	if !updated.SizeBytes.Valid || updated.SizeBytes.Int64 <= 0 {
		t.Fatal("size_bytes not set or zero")
	}

	// Verify the ZIP contents.
	rc, size, err := store.Get(ctx, updated.FilePath.String)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), size)
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}

	found := make(map[string]bool)
	for _, f := range zr.File {
		found[f.Name] = true
	}
	for _, want := range []string{"manifest.json", "profile.json", "recipes.json", "batches.json", "social.json"} {
		if !found[want] {
			t.Errorf("ZIP missing %s; got %v", want, zr.File)
		}
	}
}

// TestUserExportDispatchWorker_FailOnMissingUser verifies the export is
// marked failed (not just silently dropped) when the user row is gone.
func TestUserExportDispatchWorker_FailOnMissingUser(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	store := testutil.NewMemStore()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	q := queries.New(pool).WithTx(tx)

	user, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username: "export_fail_test",
		Email:    "export_fail_test@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	export, err := q.CreateUserExport(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Anonymize the user (set deleted_at) so GetUserByID — which filters by
	// deleted_at IS NULL — returns no rows and buildZIP fails. A hard DELETE
	// would CASCADE the user_exports row away, leaving nothing for the worker
	// to claim or the assertion to fetch.
	if _, err := tx.Exec(ctx, "UPDATE users SET deleted_at = now() WHERE id = $1", user.ID); err != nil {
		t.Fatal(err)
	}

	worker := &userExportDispatchWorker{queries: q, store: store}
	// Work should not return an error — it logs + marks individual exports
	// failed rather than failing the whole job.
	if err := worker.Work(ctx, &river.Job[UserExportDispatchArgs]{}); err != nil {
		t.Fatalf("Work returned error: %v", err)
	}

	updated, err := q.GetUserExport(ctx, queries.GetUserExportParams{
		ID:     export.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != queries.JobStatusFailed {
		t.Fatalf("status = %s, want failed", updated.Status)
	}
	if !updated.Error.Valid || updated.Error.String == "" {
		t.Fatal("error message not set on failed export")
	}
}
