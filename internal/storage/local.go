package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type localStore struct {
	root string
}

// NewLocal returns a Store backed by the local filesystem rooted at the
// given path. Used directly by the user-export pipeline (which is local-only
// regardless of STORAGE_BACKEND); the configurable backup pipeline goes
// through New() instead.
func NewLocal(root string) (Store, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create local root %q: %w", root, err)
	}
	return &localStore{root: root}, nil
}

func (s *localStore) Backend() string { return "local" }

func (s *localStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	dest := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("storage: mkdir: %w", err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("storage: create %q: %w", dest, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("storage: write %q: %w", dest, err)
	}
	return nil
}

func (s *localStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	path := filepath.Join(s.root, filepath.FromSlash(key))
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: open %q: %w", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("storage: stat %q: %w", path, err)
	}
	return f, fi.Size(), nil
}

func (s *localStore) Delete(_ context.Context, key string) error {
	path := filepath.Join(s.root, filepath.FromSlash(key))
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// PresignGet returns "" — local files are served directly by the handler.
func (s *localStore) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", nil
}
