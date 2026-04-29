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

func TestLoadParsesTrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 127.0.0.1, fd00::/8")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.TrustedProxies); got != 3 {
		t.Fatalf("got %d prefixes, want 3 (%v)", got, cfg.TrustedProxies)
	}
	// Bare IP becomes a /32 (IPv4) — the operator can write either form.
	if got, want := cfg.TrustedProxies[1].Bits(), 32; got != want {
		t.Errorf("bare 127.0.0.1 → /%d, want /%d", got, want)
	}
}

func TestLoadRejectsInvalidTrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,not-an-ip")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for malformed TRUSTED_PROXIES entry")
	}
}

func TestLoadEmptyTrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@y/z")
	t.Setenv("TRUSTED_PROXIES", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedProxies != nil {
		t.Fatalf("empty TRUSTED_PROXIES should be nil, got %v", cfg.TrustedProxies)
	}
}
