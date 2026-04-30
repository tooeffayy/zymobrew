package jobs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
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

	export, err := q.CreateUserExport(ctx, queries.CreateUserExportParams{UserID: user.ID, Format: ExportFormatZip})
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
	if !updated.Sha256.Valid || len(updated.Sha256.String) != 64 {
		t.Fatalf("sha256 not set or not 64 hex chars: %q", updated.Sha256.String)
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

	// Integrity contract: the sha256 the API exposes must equal the hash of
	// the bytes a client would download. If this assertion ever fails,
	// somebody has decoupled the upload-time hash from the actual blob.
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); want != updated.Sha256.String {
		t.Fatalf("sha256 mismatch:\n  stored: %s\n  bytes:  %s", updated.Sha256.String, want)
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

// TestUserExportDispatchWorker_Formats covers tar.gz and zstd in addition to
// zip. Each format is decompressed end-to-end and the full set of expected
// entries is asserted — this is what catches a broken Close order, where the
// outer compressor is closed before the inner tar trailer is flushed and
// later entries silently disappear.
func TestUserExportDispatchWorker_Formats(t *testing.T) {
	expected := []string{"manifest.json", "profile.json", "recipes.json", "batches.json", "social.json"}

	cases := []struct {
		format   string
		ext      string
		readArch func(t *testing.T, raw []byte) map[string]bool
	}{
		{
			format: ExportFormatTarGz,
			ext:    "tar.gz",
			readArch: func(t *testing.T, raw []byte) map[string]bool {
				gr, err := gzip.NewReader(bytes.NewReader(raw))
				if err != nil {
					t.Fatalf("gzip reader: %v", err)
				}
				defer gr.Close()
				return readTarEntries(t, gr)
			},
		},
		{
			format: ExportFormatZstd,
			ext:    "tar.zst",
			readArch: func(t *testing.T, raw []byte) map[string]bool {
				zr, err := zstd.NewReader(bytes.NewReader(raw))
				if err != nil {
					t.Fatalf("zstd reader: %v", err)
				}
				defer zr.Close()
				return readTarEntries(t, zr)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
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
				Username: "fmt_test_" + strings.ReplaceAll(tc.format, ".", "_"),
				Email:    "fmt_" + strings.ReplaceAll(tc.format, ".", "_") + "@example.com",
			})
			if err != nil {
				t.Fatal(err)
			}
			export, err := q.CreateUserExport(ctx, queries.CreateUserExportParams{
				UserID: user.ID,
				Format: tc.format,
			})
			if err != nil {
				t.Fatal(err)
			}

			worker := &userExportDispatchWorker{queries: q, store: store}
			if err := worker.Work(ctx, &river.Job[UserExportDispatchArgs]{}); err != nil {
				t.Fatalf("Work: %v", err)
			}

			updated, err := q.GetUserExport(ctx, queries.GetUserExportParams{ID: export.ID, UserID: user.ID})
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != queries.JobStatusComplete {
				t.Fatalf("status = %s, want complete (error: %s)", updated.Status, updated.Error.String)
			}
			if !strings.HasSuffix(updated.FilePath.String, "."+tc.ext) {
				t.Errorf("storage key %q missing %s extension", updated.FilePath.String, tc.ext)
			}

			rc, _, err := store.Get(ctx, updated.FilePath.String)
			if err != nil {
				t.Fatalf("store.Get: %v", err)
			}
			defer rc.Close()
			raw, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}

			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != updated.Sha256.String {
				t.Fatalf("sha256 mismatch:\n  stored: %s\n  bytes:  %s", updated.Sha256.String, got)
			}

			found := tc.readArch(t, raw)
			for _, want := range expected {
				if !found[want] {
					t.Errorf("%s archive missing %s; got %v", tc.format, want, found)
				}
			}
		})
	}
}

func readTarEntries(t *testing.T, r io.Reader) map[string]bool {
	t.Helper()
	tr := tar.NewReader(r)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		found[hdr.Name] = true
	}
	return found
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
	export, err := q.CreateUserExport(ctx, queries.CreateUserExportParams{UserID: user.ID, Format: ExportFormatZip})
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
