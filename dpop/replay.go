package dpop

import (
	"sync"
	"time"
)

// ReplayCache stores recently observed proof identifiers so that a DPoP proof
// cannot be replayed within the bounded retention window. Implementations must
// be safe for concurrent use.
type ReplayCache interface {
	// SeenBefore atomically records jti with the given expiry and reports
	// whether it was already present and unexpired.
	SeenBefore(jti string, expiresAt time.Time) bool
}

type cacheEntry struct {
	expiresAt time.Time
}

// memoryReplayCache is an in-memory bounded jti cache. Expired entries are
// evicted lazily on access and aggressively once maxSize is exceeded.
type memoryReplayCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
	maxSize int
	now     func() time.Time
}

// NewMemoryReplayCache returns an in-memory ReplayCache retaining identifiers
// for ttl. A zero ttl defaults to ten minutes.
func NewMemoryReplayCache(ttl time.Duration) ReplayCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &memoryReplayCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		maxSize: 100_000,
		now:     time.Now,
	}
}

func (c *memoryReplayCache) withClock(now func() time.Time) {
	c.now = now
}

func (c *memoryReplayCache) SeenBefore(jti string, expiresAt time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if existing, ok := c.entries[jti]; ok {
		if existing.expiresAt.After(now) {
			return true
		}
		delete(c.entries, jti)
	}

	c.entries[jti] = cacheEntry{expiresAt: expiresAt}
	if len(c.entries) > c.maxSize {
		c.evict(now)
	}
	return false
}

// evict removes expired entries and, if the cache is still over capacity,
// drops the entries nearest to expiry to keep memory bounded.
func (c *memoryReplayCache) evict(now time.Time) {
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) <= c.maxSize {
		return
	}

	over := len(c.entries) - c.maxSize
	for range over {
		var oldestKey string
		var oldestExpiry time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
				oldestKey = key
				oldestExpiry = entry.expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
}
