package storage_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"zymobrew/internal/storage"
	"zymobrew/internal/config"
)

// TestLocalStore_Roundtrip verifies that the local backend can put, get, and
// delete objects, and that content survives the round-trip intact. This is the
// same sequence the selftest storage probe runs against the configured backend.
func TestLocalStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.New(config.Config{
		StorageBackend:   "local",
		StorageLocalPath: dir,
	})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if store.Backend() != "local" {
		t.Fatalf("Backend() = %q, want local", store.Backend())
	}

	ctx := context.Background()
	key := "selftest/probe.txt"
	const payload = "zymo storage roundtrip"

	// Put.
	if err := store.Put(ctx, key, strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get — verify content.
	rc, size, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	if size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", size, len(payload))
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("content = %q, want %q", string(got), payload)
	}

	// PresignGet returns empty for local backend.
	url, err := store.PresignGet(ctx, key, 0)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if url != "" {
		t.Errorf("PresignGet = %q, want empty for local backend", url)
	}

	// Delete — should succeed.
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Get after delete should fail.
	if _, _, err := store.Get(ctx, key); err == nil {
		t.Fatal("Get after Delete: expected error, got nil")
	}

	// Second delete is a no-op (not an error).
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
}

// TestLocalStore_NestedKeys verifies that keys with path separators create
// the necessary subdirectories automatically.
func TestLocalStore_NestedKeys(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.New(config.Config{
		StorageBackend:   "local",
		StorageLocalPath: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	key := "exports/users/abc123/def456.zip"
	if err := store.Put(ctx, key, strings.NewReader("data"), 4); err != nil {
		t.Fatalf("Put nested key: %v", err)
	}
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get nested key: %v", err)
	}
	rc.Close()
}
