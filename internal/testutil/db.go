// Package testutil provides shared helpers for DB-backed tests.
//
// Tests skip if TEST_DATABASE_URL is unset. To run them locally:
//
//	docker compose up -d postgres
//	export TEST_DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
//	go test ./...
package testutil

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"zymobrew/internal/migrate"
)

var (
	once sync.Once
	pool *pgxpool.Pool
	err  error
)

// Pool returns a process-shared pgxpool with the schema migrated. Tests
// should isolate themselves with pool.Begin / Rollback. Cleanup is
// handled at end-of-process via RunWithCleanup, registered as TestMain
// in each DB-using package.
func Pool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping (see docker-compose.yml — `make db-up` then export TEST_DATABASE_URL)")
	}
	once.Do(func() {
		var sqlDB *sql.DB
		sqlDB, err = sql.Open("pgx", url)
		if err != nil {
			return
		}
		defer sqlDB.Close()
		if err = migrate.Up(ctx, sqlDB); err != nil {
			return
		}
		pool, err = pgxpool.New(ctx, url)
	})
	if err != nil {
		t.Fatalf("test db setup: %v", err)
	}
	return pool
}

// RunWithCleanup wraps testing.M.Run so a package's tests truncate
// every public table after the suite finishes. Use from a package's
// TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.RunWithCleanup(m)) }
//
// Without this hook, `go test` leaves the last test's data in the DB —
// pollutes a dev session pointed at the same DATABASE_URL. We pair it
// with the start-of-process truncate inside Pool() so a crashed run
// also self-heals on the next invocation.
//
// Skips silently when TEST_DATABASE_URL is unset (DB tests skipped) or
// the pool fails to open (no DB to clean). The cleanup error is logged
// but not surfaced through the exit code — failing the test run for a
// best-effort cleanup is more disruptive than helpful.
func RunWithCleanup(m *testing.M) int {
	code := m.Run()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		return code
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return code
	}
	defer p.Close()
	if err := truncateAppTables(ctx, p); err != nil {
		// Stderr only — don't override test exit codes.
		println("testutil: post-suite truncate failed:", err.Error())
	}
	return code
}

// truncateAppTables wipes every public table except the migration
// bookkeeping ones (goose for app schema, river_migration for the job
// queue). Built dynamically from pg_tables so future migrations are
// covered without edits here. CASCADE follows FKs; RESTART IDENTITY
// resets any sequences so seq-derived columns don't drift across runs.
//
// Concurrency model under `go test ./...`:
//   - Multiple test binaries run in parallel against the same DB.
//   - Each package's TestMain calls this on exit, but exit times overlap
//     — early finishers will hit ACCESS EXCLUSIVE contention against
//     still-running tests in other packages.
//   - The advisory lock serializes truncate-vs-truncate.
//   - lock_timeout makes truncate-vs-running-test best-effort: an
//     early-finishing process gives up if another package's tests are
//     still holding row locks. The LAST package to finish has no
//     contention and succeeds. Net result: residue is cleared exactly
//     once per `go test ./...`, by whichever process exits last.
//   - ORDER BY tablename keeps lock acquisition deterministic in case
//     two truncates ever bypass the advisory lock.
func truncateAppTables(ctx context.Context, p *pgxpool.Pool) error {
	const stmt = `
DO $$
DECLARE
  cmd text;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('zymo-test-truncate'));
  SET LOCAL lock_timeout = '500ms';
  SELECT 'TRUNCATE ' || string_agg(quote_ident(tablename), ', ' ORDER BY tablename) || ' RESTART IDENTITY CASCADE'
  INTO cmd
  FROM pg_tables
  WHERE schemaname = 'public'
    AND tablename NOT IN ('goose_db_version', 'river_migration');
  IF cmd IS NOT NULL THEN
    EXECUTE cmd;
  END IF;
END $$;`
	_, e := p.Exec(ctx, stmt)
	return e
}
