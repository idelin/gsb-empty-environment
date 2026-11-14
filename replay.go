package netsec

import (
	"sync"
	"time"
)

// ReplayCache deduplicates early-data executions by key (client
// identity + idempotency key). The first caller executes the handler;
// concurrent duplicates block until it finishes and every caller —
// including later replays within the TTL — receives the stored result
// without the handler running again.
type ReplayCache struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	entries map[string]*replayEntry
}

type replayEntry struct {
	done      chan struct{}
	result    any
	err       error
	expiresAt time.Time
}

// NewReplayCache keeps completed results for ttl so that delayed
// replays are still absorbed.
func NewReplayCache(ttl time.Duration) *ReplayCache {
	return &ReplayCache{
		now:     time.Now,
		ttl:     ttl,
		entries: make(map[string]*replayEntry),
	}
}

// Do runs fn exactly once per key. Callers arriving while fn is in
// flight, or after it completed within the retention TTL, wait for and
// share its result; shared reports whether that happened.
func (c *ReplayCache) Do(key string, fn func() (any, error)) (result any, shared bool, err error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if c.now().Before(e.expiresAt) {
			c.mu.Unlock()
			<-e.done
			return e.result, true, e.err
		}
		delete(c.entries, key)
	}
	e := &replayEntry{
		done:      make(chan struct{}),
		expiresAt: c.now().Add(c.ttl),
	}
	c.entries[key] = e
	c.mu.Unlock()

	e.result, e.err = fn()
	close(e.done)
	return e.result, false, e.err
}

// Purge drops expired entries. It is safe to call periodically from a
// janitor goroutine; entries are also evicted lazily on access.
func (c *ReplayCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}
