package wssecurity

import (
	"context"
	"sync"
	"time"
)

// NonceCache remembers consumed UsernameToken Nonce/Created pairs for their
// validity window. Implementations may be backed by memory, Redis, etc.
type NonceCache interface {
	// CheckAndStore atomically stores key when it is not already active. It
	// returns true when key was used for the first time and false on replay.
	// Entries expire after ttl so the cache does not grow without bound.
	CheckAndStore(ctx context.Context, key string, now time.Time, ttl time.Duration) (bool, error)
}

// MemoryNonceCache is a concurrency safe in-process NonceCache. Expired
// entries are evicted lazily on each call.
type MemoryNonceCache struct {
	mu         sync.Mutex
	entries    map[string]time.Time
	defaultTTL time.Duration
}

// NewMemoryNonceCache creates a cache using ttl for entries whose caller does
// not supply an explicit TTL.
func NewMemoryNonceCache(ttl time.Duration) *MemoryNonceCache {
	if ttl <= 0 {
		ttl = DefaultNonceRetention
	}
	return &MemoryNonceCache{
		entries:    make(map[string]time.Time),
		defaultTTL: ttl,
	}
}

// CheckAndStore implements NonceCache.
func (c *MemoryNonceCache) CheckAndStore(_ context.Context, key string, now time.Time, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Lazy eviction of every expired entry.
	for k, expiry := range c.entries {
		if !now.Before(expiry) {
			delete(c.entries, k)
		}
	}

	if _, exists := c.entries[key]; exists {
		return false, nil
	}
	c.entries[key] = now.Add(ttl)
	return true, nil
}
