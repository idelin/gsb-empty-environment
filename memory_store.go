package seq

import (
	"context"
	"sync"
)

// MemoryStore is a process-local Store implementation. It is safe for
// concurrent use and serves as the reference implementation for tests
// and single-process deployments.
type MemoryStore struct {
	mu   sync.Mutex
	maxv map[string]uint64
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{maxv: make(map[string]uint64)}
}

// MaxReserved implements Store.
func (s *MemoryStore) MaxReserved(_ context.Context, key string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxv[key], nil
}

// CompareAndSwapMaxReserved implements Store.
func (s *MemoryStore) CompareAndSwapMaxReserved(_ context.Context, key string, expected, next uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxv[key] != expected {
		return ErrCASConflict
	}
	s.maxv[key] = next
	return nil
}
