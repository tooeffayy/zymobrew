package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"zymobrew/internal/storage"
)

// MemStore is an in-memory storage.Store for use in tests.
type MemStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{data: make(map[string][]byte)}
}

func (s *MemStore) Backend() string { return "mem" }

func (s *MemStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("memstore put %q: %w", key, err)
	}
	s.mu.Lock()
	s.data[key] = b
	s.mu.Unlock()
	return nil
}

func (s *MemStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	s.mu.RLock()
	b, ok := s.data[key]
	s.mu.RUnlock()
	if !ok {
		return nil, 0, fmt.Errorf("memstore: key not found: %q", key)
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (s *MemStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}

func (s *MemStore) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", nil
}

// Has reports whether key exists.
func (s *MemStore) Has(key string) bool {
	s.mu.RLock()
	_, ok := s.data[key]
	s.mu.RUnlock()
	return ok
}

// Keys returns a snapshot of all stored keys.
func (s *MemStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

var _ storage.Store = (*MemStore)(nil)
