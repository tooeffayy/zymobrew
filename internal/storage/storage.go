// Package storage provides a backend-agnostic file store used for user
// exports and admin backups. Two stores are wired at runtime — the primary
// store (governed by STORAGE_*) holds user-export archives; a separately
// configurable backup store (governed by BACKUP_*, falling back to STORAGE_*
// when unset) holds admin pg_dumps.
package storage

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Store is the interface all backends implement.
type Store interface {
	// Put writes r to the given key. size is the expected byte count; pass -1
	// if unknown (e.g. a piped pg_dump whose size isn't known up front).
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a reader for the stored object and its size in bytes.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// Delete removes the object at key. Implementations should treat a
	// missing key as a no-op (not an error).
	Delete(ctx context.Context, key string) error

	// PresignGet returns a time-limited URL for direct download, or "" if the
	// backend doesn't support presigning (caller must stream via Get instead).
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)

	// Backend returns the backend name ("local", "s3").
	Backend() string
}

// BackendConfig collapses the env-var fields needed to construct a single
// Store. The primary and backup stores each consume one of these — see
// config.Config.PrimaryBackend / BackupBackend. Decoupling storage from the
// full Config lets the same constructor build either pipeline's backend.
type BackendConfig struct {
	Backend     string // "local" | "s3"
	LocalPath   string
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
}

// New returns a Store configured from bc.
func New(bc BackendConfig) (Store, error) {
	switch bc.Backend {
	case "local", "":
		return NewLocal(bc.LocalPath)
	case "s3":
		return newS3(bc)
	default:
		return nil, fmt.Errorf("unknown storage backend %q (want local|s3)", bc.Backend)
	}
}
