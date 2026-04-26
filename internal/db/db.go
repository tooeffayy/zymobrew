package db

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// NewPool opens a pgx connection pool for application queries.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, url)
}

// OpenSQLDB opens a database/sql handle backed by the pgx driver. Used by the
// migration runner, which speaks database/sql.
func OpenSQLDB(url string) (*sql.DB, error) {
	return sql.Open("pgx", url)
}
