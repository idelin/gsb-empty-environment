package jitterbuf

import (
	"sync"
	"time"
)

// Clock is the time source used by the jitter buffer. Production code can use
// a wall clock; tests drive deterministic arrival/playout sequences with
// VirtualClock.
type Clock interface {
	Now() time.Time
}

// WallClock reports the real current time.
type WallClock struct{}

// Now implements Clock.
func (WallClock) Now() time.Time { return time.Now() }

// VirtualClock is a manually controlled time source. Time only moves when
// Advance (or Set) is called, which makes arrival sequences and playout
// decisions fully deterministic in tests.
type VirtualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewVirtualClock returns a virtual clock initialized at t. A zero time is
// replaced with time.Unix(0, 0) so duration arithmetic stays well defined.
func NewVirtualClock(t time.Time) *VirtualClock {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	return &VirtualClock{now: t}
}

// Now returns the current virtual time.
func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the virtual clock forward by d. Negative or zero durations
// are ignored.
func (c *VirtualClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set jumps the virtual clock to t.
func (c *VirtualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
