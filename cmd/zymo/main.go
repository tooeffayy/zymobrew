package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"zymobrew/internal/account"
	"zymobrew/internal/config"
	"zymobrew/internal/db"
	"zymobrew/internal/jobs"
	"zymobrew/internal/migrate"
	"zymobrew/internal/queries"
	"zymobrew/internal/selftest"
	"zymobrew/internal/server"
	"zymobrew/internal/storage"
)

const usage = `zymo — fermentation tracking server

usage:
  zymo serve                run the HTTP server (default; auto-migrates unless AUTO_MIGRATE=false)
  zymo migrate              apply pending migrations and exit
  zymo selftest             run runtime smoke tests against the configured database
  zymo vapid-keys           generate a VAPID key pair and print them as env vars
  zymo reprocess-deletions  re-anonymize accounts whose deletion was undone by a backup restore
  zymo version              print version
`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		run(ctx, serve)
	case "migrate":
		run(ctx, runMigrate)
	case "selftest":
		run(ctx, func(ctx context.Context, cfg config.Config) error {
			return selftest.Run(ctx, cfg, os.Stdout)
		})
	case "vapid-keys":
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating VAPID keys: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
	case "reprocess-deletions":
		run(ctx, reprocessDeletions)
	case "version":
		fmt.Println("zymo dev")
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func run(ctx context.Context, fn func(context.Context, config.Config) error) {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := fn(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func serve(ctx context.Context, cfg config.Config) error {
	if cfg.AutoMigrate {
		if err := runMigrate(ctx, cfg); err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
	}
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer pool.Close()

	exportStore, backupStore, err := openStores(cfg)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	jobsClient, err := jobs.New(pool, cfg, exportStore, backupStore)
	if err != nil {
		return fmt.Errorf("jobs init: %w", err)
	}
	if err := jobsClient.Start(ctx); err != nil {
		return fmt.Errorf("jobs start: %w", err)
	}
	defer func() {
		// Stop with a fresh ctx — the parent is already cancelled by the time
		// the deferred Stop runs, and River needs time to drain.
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = jobsClient.Stop(stopCtx)
	}()

	srv := server.New(pool, cfg, exportStore, backupStore)
	log.Printf("zymo listening on %s (mode=%s)", cfg.ListenAddr, cfg.InstanceMode)
	return srv.Run(ctx, cfg.ListenAddr)
}

func runMigrate(ctx context.Context, cfg config.Config) error {
	sqlDB, err := db.OpenSQLDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	return migrate.Up(ctx, sqlDB)
}

// reprocessDeletions re-runs anonymization for any account_deletion_requests
// rows whose user is not currently soft-deleted — the case after restoring
// a backup taken before the user's deletion was processed.
func reprocessDeletions(ctx context.Context, cfg config.Config) error {
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer pool.Close()

	exportStore, _, err := openStores(cfg)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	q := queries.New(pool)
	pending, err := q.ListUnprocessedDeletionRequests(ctx)
	if err != nil {
		return fmt.Errorf("list unprocessed: %w", err)
	}
	if len(pending) == 0 {
		fmt.Println("no pending deletions to reprocess")
		return nil
	}

	failures := 0
	for _, row := range pending {
		if err := account.Anonymize(ctx, pool, q, exportStore, row.UserID); err != nil {
			fmt.Fprintf(os.Stderr, "user %s: %v\n", row.UserID, err)
			failures++
			continue
		}
		fmt.Printf("anonymized %s (request %s)\n", row.UserID, row.ID)
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d deletions failed", failures, len(pending))
	}
	return nil
}

// openStores constructs the two storage backends used at runtime:
//
//   - exportStore: the primary storage backend (governed by STORAGE_*).
//     User exports live under the `tmp/exports/` key prefix to convey their
//     ephemeral lifecycle (deleted on download or after the per-row TTL).
//   - backupStore: the admin-backup backend (governed by BACKUP_*, falling
//     back to STORAGE_* when unset). Holds pg_dump archives.
func openStores(cfg config.Config) (storage.Store, storage.Store, error) {
	exportStore, err := storage.New(cfg.PrimaryStorage())
	if err != nil {
		return nil, nil, fmt.Errorf("primary store: %w", err)
	}
	backupStore, err := storage.New(cfg.BackupStorage())
	if err != nil {
		return nil, nil, fmt.Errorf("backup store: %w", err)
	}
	return exportStore, backupStore, nil
}
