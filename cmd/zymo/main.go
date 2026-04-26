package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"zymobrew/internal/config"
	"zymobrew/internal/db"
	"zymobrew/internal/migrate"
	"zymobrew/internal/selftest"
	"zymobrew/internal/server"
)

const usage = `zymo — fermentation tracking server

usage:
  zymo serve      run the HTTP server (default; auto-migrates unless AUTO_MIGRATE=false)
  zymo migrate    apply pending migrations and exit
  zymo selftest   run runtime smoke tests against the configured database
  zymo version    print version
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

	srv := server.New(pool)
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
