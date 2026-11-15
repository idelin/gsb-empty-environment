package netsec

import (
	"sync"
	"time"
)

// NonceReplayStore records a UsernameToken Nonce together with its Created
// value. CheckAndStore must check and insert atomically so concurrent requests
// cannot both accept the same token.
//
// now is the Validator's clock value for the current request. Implementations
// should use it, rather than time.Now, so an injected clock is honoured.
type NonceReplayStore interface {
	CheckAndStore(nonce string, created, expiresAt, now time.Time) (bool, error)
}

// MemoryNonceReplayStore is a concurrency-safe in-memory NonceReplayStore.
// Expired entries are removed lazily.
type MemoryNonceReplayStore struct {
	mu      sync.Mutex
	entries map[replayKey]time.Time
}

type replayKey struct {
	nonce   string
	created int64
}

// NewMemoryNonceReplayStore returns an empty in-memory replay store.
func NewMemoryNonceReplayStore() *MemoryNonceReplayStore {
	return &MemoryNonceReplayStore{entries: make(map[replayKey]time.Time)}
}

// CheckAndStore reports whether this nonce/Created pair is being used for the
// first time. The entry is kept until expiresAt.
func (store *MemoryNonceReplayStore) CheckAndStore(nonce string, created, expiresAt, now time.Time) (bool, error) {
	key := replayKey{nonce: nonce, created: created.UnixNano()}

	store.mu.Lock()
	defer store.mu.Unlock()

	if store.entries == nil {
		store.entries = make(map[replayKey]time.Time)
	}
	for existingKey, existingExpiry := range store.entries {
		if !existingExpiry.After(now) {
			delete(store.entries, existingKey)
		}
	}

	if expiry, exists := store.entries[key]; exists && expiry.After(now) {
		return false, nil
	}

	store.entries[key] = expiresAt
	return true, nil
}
