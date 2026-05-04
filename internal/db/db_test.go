package db_test

import (
	"context"
	"testing"

	"zymobrew/internal/testutil"
)

// TestSchemaTablesPresent asserts the schema migration created the expected
// number of tables. Acts as a tripwire: if a future migration drops or adds
// tables, this catches the count drift.
func TestSchemaTablesPresent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	// Tripwire scoped to *our* schema. River manages its own version table
	// (`river_migration`) and adds/removes its tables across upgrades, so
	// counting them here would be flaky. Same for goose's bookkeeping.
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema='public' AND table_type='BASE TABLE'
		   AND table_name <> 'goose_db_version'
		   AND table_name NOT LIKE 'river\_%' ESCAPE '\'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	const want = 29 // bumped in 0009_inventory.sql (added inventory_items)
	if n != want {
		t.Fatalf("expected %d app tables, got %d", want, n)
	}
}

// TestRecipeRevisionCircularFK exercises the recipes ↔ recipe_revisions
// circular FK by inserting a recipe, its first revision, and pointing
// recipes.current_revision_id back at the new revision.
func TestRecipeRevisionCircularFK(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var userID, recipeID, revID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (username, email) VALUES ('circ_user', 'circ@example.com') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO recipes (author_id, brew_type, name) VALUES ($1, 'mead', 'Circ') RETURNING id`,
		userID,
	).Scan(&recipeID); err != nil {
		t.Fatalf("insert recipe: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO recipe_revisions (recipe_id, revision_number, author_id, name, ingredients)
		 VALUES ($1, 1, $2, 'Circ', '[]'::jsonb) RETURNING id`,
		recipeID, userID,
	).Scan(&revID); err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE recipes SET current_revision_id=$1 WHERE id=$2`, revID, recipeID,
	); err != nil {
		t.Fatalf("set current_revision_id: %v", err)
	}
}

// TestFollowsSelfReferenceBlocked verifies the CHECK constraint that prevents
// users from following themselves.
func TestFollowsSelfReferenceBlocked(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var userID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (username, email) VALUES ('self_follow', 'sf@example.com') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO follows (follower_id, followed_id) VALUES ($1, $1)`, userID,
	)
	if err == nil {
		t.Fatal("expected self-follow to be rejected by CHECK constraint")
	}
}
