package config_test

import (
	"testing"

	"zymobrew/internal/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("INSTANCE_MODE", "")
	t.Setenv("AUTO_MIGRATE", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.InstanceMode != config.ModeSingleUser {
		t.Errorf("InstanceMode = %q, want single_user", cfg.InstanceMode)
	}
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate should default to true")
	}
}

func TestLoadRejectsInvalidMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	t.Setenv("INSTANCE_MODE", "bogus")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for invalid INSTANCE_MODE")
	}
}
