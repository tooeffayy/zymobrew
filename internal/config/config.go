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
	DatabaseURL     string
	ListenAddr      string
	InstanceMode    InstanceMode
	AutoMigrate     bool
	CookieSecure    bool
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string

	// Storage
	StorageBackend    string // "local" | "s3"
	StorageLocalPath  string
	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKey       string
	S3SecretKey       string
	BackupRetentionDays int
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ListenAddr:      getenv("LISTEN_ADDR", ":8080"),
		InstanceMode:    InstanceMode(getenv("INSTANCE_MODE", string(ModeSingleUser))),
		AutoMigrate:     getenvBool("AUTO_MIGRATE", true),
		CookieSecure:    getenvBool("COOKIE_SECURE", false),
		VAPIDPublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:    getenv("VAPID_SUBJECT", "mailto:admin@localhost"),

		StorageBackend:      getenv("STORAGE_BACKEND", "local"),
		StorageLocalPath:    getenv("STORAGE_LOCAL_PATH", "./data"),
		S3Endpoint:          os.Getenv("S3_ENDPOINT"),
		S3Region:            getenv("S3_REGION", "us-east-1"),
		S3Bucket:            os.Getenv("S3_BUCKET"),
		S3AccessKey:         os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:         os.Getenv("S3_SECRET_KEY"),
		BackupRetentionDays: getenvInt("BACKUP_RETENTION_DAYS", 30),
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

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
