// Package selftest is the runtime smoke test for a deployed zymo instance.
// `zymo selftest` exits non-zero if anything fails — handy for ops to verify
// a fresh deploy before pointing traffic at it.
package selftest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"zymobrew/internal/config"
	"zymobrew/internal/db"
	"zymobrew/internal/migrate"
)

type check struct {
	name string
	ok   bool
	msg  string
}

// Run executes the smoke checks against the configured database and writes a
// human-readable report to out. Returns an error if any check fails.
func Run(ctx context.Context, cfg config.Config, out io.Writer) error {
	checks := []check{}
	add := func(c check) { checks = append(checks, c) }
	defer func() { report(out, checks) }()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		add(check{"connect", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	defer pool.Close()
	add(check{"connect", true, ""})

	if err := pool.Ping(ctx); err != nil {
		add(check{"ping", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	add(check{"ping", true, ""})

	sqlDB, err := db.OpenSQLDB(cfg.DatabaseURL)
	if err != nil {
		add(check{"migrations", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	defer sqlDB.Close()

	version, err := migrate.Version(ctx, sqlDB)
	if err != nil {
		add(check{"migrations", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	add(check{"migrations", true, fmt.Sprintf("version=%d", version)})

	if err := schemaCheck(ctx, pool); err != nil {
		add(check{"schema", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	add(check{"schema", true, ""})

	if err := crudRoundtrip(ctx, pool); err != nil {
		add(check{"roundtrip", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	add(check{"roundtrip", true, ""})

	return nil
}

func schemaCheck(ctx context.Context, pool *pgxpool.Pool) error {
	expected := []string{
		"users", "sessions", "recipes", "recipe_revisions", "batches",
		"readings", "batch_events", "tasting_notes", "reminders",
		"notifications", "devices", "batch_devices",
		// River background-job runtime — verifies migration ran end-to-end.
		"river_job", "river_migration",
	}
	for _, table := range expected {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                 WHERE table_schema='public' AND table_name=$1)`,
			table).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("missing table: %s", table)
		}
	}
	return nil
}

// crudRoundtrip exercises the full timeline write path inside a transaction
// that is rolled back, so it leaves no rows behind. Catches FK / enum / NOT
// NULL drift between schema and assumptions.
func crudRoundtrip(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	suffix, err := randomSuffix()
	if err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	var userID, recipeID, revID, batchID string

	if err := tx.QueryRow(ctx,
		`INSERT INTO users (username, email) VALUES ($1, $2) RETURNING id`,
		"selftest_"+suffix, "selftest_"+suffix+"@example.com").Scan(&userID); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO recipes (author_id, brew_type, name) VALUES ($1, 'mead', 'selftest') RETURNING id`,
		userID).Scan(&recipeID); err != nil {
		return fmt.Errorf("insert recipe: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO recipe_revisions (recipe_id, revision_number, author_id, name, ingredients)
		 VALUES ($1, 1, $2, 'selftest', '[]'::jsonb) RETURNING id`,
		recipeID, userID).Scan(&revID); err != nil {
		return fmt.Errorf("insert revision: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE recipes SET current_revision_id=$1, revision_count=1 WHERE id=$2`,
		revID, recipeID); err != nil {
		return fmt.Errorf("set current revision: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO batches (brewer_id, recipe_id, recipe_revision_id, name, brew_type, stage)
		 VALUES ($1, $2, $3, 'selftest', 'mead', 'primary') RETURNING id`,
		userID, recipeID, revID).Scan(&batchID); err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO readings (batch_id, taken_at, gravity, temperature_c) VALUES ($1, now(), 1.105, 21.5)`,
		batchID); err != nil {
		return fmt.Errorf("insert reading: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO batch_events (batch_id, occurred_at, kind, title) VALUES ($1, now(), 'pitch', 'selftest pitch')`,
		batchID); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func report(out io.Writer, checks []check) {
	for _, c := range checks {
		mark := "FAIL"
		if c.ok {
			mark = " OK "
		}
		if c.msg != "" {
			fmt.Fprintf(out, "[%s] %-12s %s\n", mark, c.name, c.msg)
		} else {
			fmt.Fprintf(out, "[%s] %s\n", mark, c.name)
		}
	}
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
