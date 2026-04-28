// Package storage provides a backend-agnostic file store used for user
// exports and admin backups. The local backend is always available; the S3
// backend is opt-in via STORAGE_BACKEND=s3.
package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"zymobrew/internal/config"
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

// New returns a Store configured from cfg.
func New(cfg config.Config) (Store, error) {
	switch cfg.StorageBackend {
	case "local", "":
		return newLocal(cfg.StorageLocalPath)
	case "s3":
		return newS3(cfg)
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND %q (want local|s3)", cfg.StorageBackend)
	}
}
