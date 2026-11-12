package txnlease

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can drive lease expiry with a virtual clock.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock backed by time.Now.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// ManualClock is a virtual clock that only moves when Advance is called.
// It is safe for concurrent use.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock returns a ManualClock pinned at start.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now implements Clock.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the virtual clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
