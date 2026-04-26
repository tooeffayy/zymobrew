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
func Up(ctx context.Context, db *sql.DB) error {
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
