package config

import (
	"fmt"
	"os"
	"strconv"
)

type InstanceMode string

const (
	ModeSingleUser InstanceMode = "single_user"
	ModeClosed     InstanceMode = "closed"
	ModeOpen       InstanceMode = "open"
)

type Config struct {
	DatabaseURL  string
	ListenAddr   string
	InstanceMode InstanceMode
	AutoMigrate  bool
	CookieSecure bool
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		ListenAddr:   getenv("LISTEN_ADDR", ":8080"),
		InstanceMode: InstanceMode(getenv("INSTANCE_MODE", string(ModeSingleUser))),
		AutoMigrate:  getenvBool("AUTO_MIGRATE", true),
		CookieSecure: getenvBool("COOKIE_SECURE", false),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	switch cfg.InstanceMode {
	case ModeSingleUser, ModeClosed, ModeOpen:
	default:
		return cfg, fmt.Errorf("invalid INSTANCE_MODE %q (want single_user|closed|open)", cfg.InstanceMode)
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
