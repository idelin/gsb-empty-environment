package netsec

import "sync"

// IdempotencyStore records idempotency keys that have already been accepted.
// Implementations must make CheckAndStore atomic: concurrent deliveries of
// the same key may only succeed once.
type IdempotencyStore interface {
	// CheckAndStore atomically records key and reports whether it is new.
	// It returns true the first time a key is seen and false afterwards.
	CheckAndStore(key string) (bool, error)
}

// MemoryIdempotencyStore is a concurrency-safe in-memory [IdempotencyStore].
// It suits single-instance runtimes; distributed runtimes should supply a
// store backed by shared storage with the same atomicity guarantees.
type MemoryIdempotencyStore struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewMemoryIdempotencyStore creates an empty in-memory idempotency store.
func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{seen: make(map[string]struct{})}
}

// CheckAndStore implements [IdempotencyStore].
func (s *MemoryIdempotencyStore) CheckAndStore(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return false, nil
	}
	s.seen[key] = struct{}{}
	return true, nil
}
