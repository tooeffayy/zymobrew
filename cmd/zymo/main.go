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

	"zymobrew/internal/config"
	"zymobrew/internal/db"
	"zymobrew/internal/jobs"
	"zymobrew/internal/migrate"
	"zymobrew/internal/selftest"
	"zymobrew/internal/server"
)

const usage = `zymo — fermentation tracking server

usage:
  zymo serve       run the HTTP server (default; auto-migrates unless AUTO_MIGRATE=false)
  zymo migrate     apply pending migrations and exit
  zymo selftest    run runtime smoke tests against the configured database
  zymo vapid-keys  generate a VAPID key pair and print them as env vars
  zymo version     print version
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

	jobsClient, err := jobs.New(pool, cfg)
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

	srv := server.New(pool, cfg)
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
