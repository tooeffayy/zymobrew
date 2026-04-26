package queries_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/queries"
	"zymobrew/internal/testutil"
)

func TestUserQueriesRoundtrip(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	q := queries.New(tx)

	suffix := randomSuffix(t)
	created, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username:    "qtest_" + suffix,
		Email:       "qtest_" + suffix + "@example.com",
		DisplayName: pgtype.Text{String: "Q Tester", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID.String() == "" {
		t.Fatal("expected non-empty UUID")
	}

	got, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Username != created.Username {
		t.Errorf("username mismatch: got %q, want %q", got.Username, created.Username)
	}

	byUsername, err := q.GetUserByUsername(ctx, created.Username)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if byUsername.ID != created.ID {
		t.Errorf("id mismatch via username lookup")
	}

	_, err = q.GetUserByUsername(ctx, "does_not_exist_"+suffix)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows for missing user, got %v", err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
