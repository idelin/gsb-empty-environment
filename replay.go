package netsec

import (
	"sync"
	"time"
)

// ReplayWindow is the required jti retention interval. A jti observed within
// this window is rejected as a replay.
const ReplayWindow = 10 * time.Minute

// ReplayCache is a concurrency-safe, in-memory record of recently observed
// token identifiers. Replay keys are scoped by issuer so that unrelated
// issuers may safely reuse a jti value.
type ReplayCache struct {
	mu        sync.Mutex
	seen      map[string]time.Time
	lastPrune time.Time
}

// NewReplayCache creates an empty replay cache using ReplayWindow.
func NewReplayCache() *ReplayCache {
	return &ReplayCache{seen: make(map[string]time.Time)}
}

// CheckAndRecord atomically checks whether issuer/jti was observed within the
// replay window and, if it was not, records the observation at now. It returns
// true when the identifier is a replay.
func (c *ReplayCache) CheckAndRecord(issuer, jti string, now time.Time) bool {
	if c == nil {
		panic("netsec: nil ReplayCache")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.seen == nil {
		c.seen = make(map[string]time.Time)
	}

	key := issuer + "\x00" + jti
	if previous, ok := c.seen[key]; ok && now.Sub(previous) < ReplayWindow {
		return true
	}

	c.seen[key] = now

	if c.lastPrune.IsZero() || now.Sub(c.lastPrune) >= time.Minute || now.Before(c.lastPrune) {
		c.pruneLocked(now)
		c.lastPrune = now
	}
	return false
}

// Seen reports whether issuer/jti is currently inside the replay window. It
// does not modify the cache.
func (c *ReplayCache) Seen(issuer, jti string, now time.Time) bool {
	if c == nil {
		panic("netsec: nil ReplayCache")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	previous, ok := c.seen[issuer+"\x00"+jti]
	return ok && now.Sub(previous) < ReplayWindow
}

// Len returns the number of replay records currently retained. Expired
// records may still be present until the next opportunistic prune.
func (c *ReplayCache) Len() int {
	if c == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

func (c *ReplayCache) pruneLocked(now time.Time) {
	for key, observedAt := range c.seen {
		if now.Sub(observedAt) >= ReplayWindow {
			delete(c.seen, key)
		}
	}
}
