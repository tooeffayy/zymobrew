package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
	"zymobrew/internal/testutil"
)

// TestExpiredSessionsWorker drives the worker directly (no River runtime):
// seed three sessions with mixed expiries, run Work, assert only the
// past-dated ones are gone.
func TestExpiredSessionsWorker(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	q := queries.New(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	qtx := q.WithTx(tx)

	user, err := qtx.CreateUser(ctx, queries.CreateUserParams{
		Username: "gc_user", Email: "gc@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	mkSession := func(label string, expiresAt time.Time) {
		t.Helper()
		_, err := qtx.CreateSession(ctx, queries.CreateSessionParams{
			UserID:    user.ID,
			TokenHash: "hash_" + label,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
		})
		if err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}
	mkSession("expired1", time.Now().Add(-time.Hour))
	mkSession("expired2", time.Now().Add(-time.Minute))
	mkSession("fresh", time.Now().Add(time.Hour))

	worker := &expiredSessionsWorker{queries: qtx}
	if err := worker.Work(ctx, &river.Job[ExpiredSessionsArgs]{Args: ExpiredSessionsArgs{}}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var remaining int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, user.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("expected 1 session remaining (the fresh one), got %d", remaining)
	}
}
