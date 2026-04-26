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
// should isolate themselves with pool.Begin / Rollback.
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
