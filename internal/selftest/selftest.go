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
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"zymobrew/internal/config"
	"zymobrew/internal/db"
	"zymobrew/internal/migrate"
	"zymobrew/internal/storage"
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

	primaryCfg := cfg.PrimaryStorage()
	backend, err := storageProbe(ctx, primaryCfg, "selftest/primary-")
	if err != nil {
		add(check{"storage", false, err.Error()})
		return fmt.Errorf("selftest failed")
	}
	add(check{"storage", true, "backend=" + backend})

	// Probe the backup store too, but only if it actually differs from the
	// primary — when BACKUP_* is unset everything falls back to STORAGE_* and
	// re-probing the same backend just costs extra round-trips. Catches the
	// "operator set BACKUP_S3_BUCKET=other but forgot the access key" case
	// before the next backup tick (up to 24h later).
	backupCfg := cfg.BackupStorage()
	if backupCfg != primaryCfg {
		backupBackend, err := storageProbe(ctx, backupCfg, "selftest/backup-")
		if err != nil {
			add(check{"backup-storage", false, err.Error()})
			return fmt.Errorf("selftest failed")
		}
		add(check{"backup-storage", true, "backend=" + backupBackend})
	}

	return nil
}

func schemaCheck(ctx context.Context, pool *pgxpool.Pool) error {
	expected := []string{
		"users", "sessions", "recipes", "recipe_revisions", "batches",
		"readings", "batch_events", "tasting_notes", "reminders",
		"notifications", "devices", "batch_devices",
		"notification_prefs", "push_devices",
		"recipe_reminder_templates", "recipe_likes", "recipe_comments", "follows",
		"user_exports", "admin_backups",
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
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_exports (user_id, status) VALUES ($1, 'pending')`,
		userID); err != nil {
		return fmt.Errorf("insert user_export: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO admin_backups (status, storage_backend) VALUES ('pending', 'local')`); err != nil {
		return fmt.Errorf("insert admin_backup: %w", err)
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

// storageProbe verifies that the given storage backend is reachable and
// writable: it writes a small probe object, reads it back to confirm the
// content round-trips correctly, then deletes it. keyPrefix scopes the
// probe key so primary + backup probes don't collide when both point at
// the same bucket.
func storageProbe(ctx context.Context, bc storage.BackendConfig, keyPrefix string) (backend string, err error) {
	store, err := storage.New(bc)
	if err != nil {
		return "", fmt.Errorf("init: %w", err)
	}

	suffix, err := randomSuffix()
	if err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	key := keyPrefix + "probe-" + suffix + ".txt"
	const payload = "zymo selftest storage probe"

	if err := store.Put(ctx, key, strings.NewReader(payload), int64(len(payload))); err != nil {
		return "", fmt.Errorf("put: %w", err)
	}
	// Always clean up, even if the read fails.
	defer func() {
		if delErr := store.Delete(ctx, key); delErr != nil && err == nil {
			err = fmt.Errorf("delete probe: %w", delErr)
		}
	}()

	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("get: %w", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if string(got) != payload {
		return "", fmt.Errorf("content mismatch: got %q, want %q", string(got), payload)
	}

	return store.Backend(), nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
