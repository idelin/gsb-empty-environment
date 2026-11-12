package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// NonceStore atomically records a nonce and reports whether it was unused.
//
// A Consume implementation must be safe for concurrent use and must return
// false for every use of a nonce after the first successful one. Implementations
// that need to survive process restarts can use a database-backed store.
type NonceStore interface {
	Consume(nonce string) (fresh bool, err error)
}

// MemoryNonceStore is an in-memory, concurrency-safe NonceStore. A single
// instance must be shared by all pushes handled by a Verifier; creating a new
// instance for every push defeats replay protection.
type MemoryNonceStore struct {
	mu   sync.Mutex
	used map[string]struct{}
}

// NewMemoryNonceStore returns an empty in-memory nonce store.
func NewMemoryNonceStore() *MemoryNonceStore {
	return &MemoryNonceStore{used: make(map[string]struct{})}
}

// Consume records nonce. It returns false when nonce has already been
// consumed.
func (s *MemoryNonceStore) Consume(nonce string) (bool, error) {
	if nonce == "" {
		return false, ErrMissingNonce
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.used[nonce]; exists {
		return false, nil
	}
	s.used[nonce] = struct{}{}
	return true, nil
}

var (
	// ErrNonceReused indicates that the push certificate's nonce was already
	// consumed by a previous push.
	ErrNonceReused = errors.New("netsec: push certificate nonce was already used")

	// ErrNonceStore indicates a failure while consulting the nonce store.
	ErrNonceStore = errors.New("netsec: nonce store failure")
)

func consumeNonce(store NonceStore, nonce string) error {
	fresh, err := store.Consume(nonce)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNonceStore, err)
	}
	if !fresh {
		return ErrNonceReused
	}
	return nil
}
