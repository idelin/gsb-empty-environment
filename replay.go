package netsec

import (
	"sync"
	"time"
)

// MemoryReplayCache is a process-local, concurrency-safe replay cache.
type MemoryReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

// NewMemoryReplayCache creates an empty replay cache.
func NewMemoryReplayCache() *MemoryReplayCache {
	return &MemoryReplayCache{entries: make(map[string]time.Time)}
}

// CheckAndStore atomically records jti until expires and reports whether it was
// already present.
func (c *MemoryReplayCache) CheckAndStore(jti string, expires time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}

	now := time.Now()
	for key, expiry := range c.entries {
		if !expiry.After(now) {
			delete(c.entries, key)
		}
	}

	if expiry, ok := c.entries[jti]; ok && expiry.After(now) {
		return true, nil
	}
	c.entries[jti] = expires
	return false, nil
}
