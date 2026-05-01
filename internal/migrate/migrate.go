package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"

	"zymobrew/migrations"
)

func init() {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
}

// Up applies all pending migrations: app schema first (goose), then the River
// background-job schema. Both must succeed before the app is ready.
//
// A Postgres session advisory lock serializes concurrent callers against the
// same database — necessary because `go test ./...` runs each package's
// TestMain in its own process, and goose itself doesn't lock. Without this,
// two processes can both observe goose_db_version=N and both try to apply
// N+1, with the loser failing on duplicate-create. The lock is held on a
// dedicated connection so it doesn't interfere with goose's own connection
// usage; the loser blocks at pg_advisory_lock until the winner finishes,
// then runs goose to find everything already applied (idempotent).
func Up(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire lock connection: %w", err)
	}
	defer func() {
		// Release on a background context so a cancelled caller still
		// unlocks before the connection returns to the pool — otherwise
		// a future borrower of that backend could inherit the held lock.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtext('zymo-migrate'))")
		conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext('zymo-migrate'))"); err != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	return riverUp(ctx, db)
}

// Version returns the current goose migration version (0 if none applied).
// River version is tracked separately and is not surfaced here.
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	return goose.GetDBVersionContext(ctx, db)
}

// Status writes a human-readable migration status to goose's logger.
func Status(ctx context.Context, db *sql.DB) error {
	return goose.StatusContext(ctx, db, ".")
}

// riverUp applies River's bundled migrations using its own migrator. River
// owns its own version table (`river_migration`) so this is idempotent and
// safe to call on every start-up.
func riverUp(ctx context.Context, db *sql.DB) error {
	driver := riverdatabasesql.New(db)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return fmt.Errorf("river migrator init: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	return nil
}
