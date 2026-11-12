package netsec

import (
	"sync"
	"time"
)

// NonceStore enforces single use of a UsernameToken (Nonce, Created) pair.
// CheckAndStore must atomically reject an existing live key and otherwise
// retain the key for ttl. Backed implementations (Redis, Memcached, ...) can
// implement this interface with SET NX EX semantics.
type NonceStore interface {
	CheckAndStore(key string, ttl time.Duration) error
}

// MemoryNonceStore is an in-process NonceStore. It is safe for concurrent
// gateways in a single process and evicts entries lazily on each call; it is
// the reference implementation and suitable where all traffic lands in one
// process.
type MemoryNonceStore struct {
	mu      sync.Mutex
	entries map[string]time.Time

	// Now is the time source. Tests may override it; production code leaves
	// the default time.Now.
	Now func() time.Time
}

// NewMemoryNonceStore constructs an empty MemoryNonceStore.
func NewMemoryNonceStore() *MemoryNonceStore {
	return &MemoryNonceStore{
		entries: make(map[string]time.Time),
		Now:     time.Now,
	}
}

// CheckAndStore rejects a key whose entry has not yet expired, otherwise
// stores it with the supplied lifetime. Expired entries are swept during the
// same locked section, so the map cannot grow unbounded under steady
// request volume.
func (s *MemoryNonceStore) CheckAndStore(key string, ttl time.Duration) error {
	if s.entries == nil {
		s.entries = make(map[string]time.Time)
	}
	if s.Now == nil {
		s.Now = time.Now
	}

	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	for existingKey, expiry := range s.entries {
		if !now.Before(expiry) {
			delete(s.entries, existingKey)
		}
	}

	if expiry, ok := s.entries[key]; ok && now.Before(expiry) {
		return fail(ErrReplay, "nonce already used within its validity window")
	}
	s.entries[key] = now.Add(ttl)
	return nil
}

// Len reports the number of currently retained entries, including entries not
// yet swept. It is primarily intended for tests and diagnostics.
func (s *MemoryNonceStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
