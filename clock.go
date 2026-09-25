package ttlcache

import (
	"sync"
	"time"
)

// ManualClock is an injectable, controllable clock for tests and demos.
// Time only moves when Advance is called, making expiry windows deterministic.
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewManualClock returns a ManualClock starting at t.
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{t: t}
}

// Now implements the clock signature expected by Config.Now.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
