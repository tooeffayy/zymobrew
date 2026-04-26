package migrate

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"

	"zymobrew/migrations"
)

func init() {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
}

// Up applies all pending migrations.
func Up(ctx context.Context, db *sql.DB) error {
	return goose.UpContext(ctx, db, ".")
}

// Version returns the current migration version (0 if none applied).
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	return goose.GetDBVersionContext(ctx, db)
}

// Status writes a human-readable migration status to goose's logger.
func Status(ctx context.Context, db *sql.DB) error {
	return goose.StatusContext(ctx, db, ".")
}
