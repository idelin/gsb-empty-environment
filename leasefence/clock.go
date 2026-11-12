package leasefence

import (
	"sync"
	"time"
)

// Clock is the time source used by a Fence. Production callers pass
// SystemClock; tests pass a ManualClock so lease expiry and late reaping are
// fully deterministic.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the host wall clock.
type SystemClock struct{}

// Now returns time.Now.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// ManualClock is a controllable, concurrency-safe clock. Advancing the clock
// never invokes any Fence callback; callers drive the reaper explicitly so
// tests can interleave blocked commits and expiry precisely.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock returns a manual clock set to t.
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{now: t}
}

// Now returns the current fake time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and returns the new time.
func (c *ManualClock) Advance(d time.Duration) time.Time {
	if d < 0 {
		panic("leasefence: cannot advance the clock backwards")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Set moves the clock to t. It panics if t is earlier than the current time,
// keeping the manual clock itself monotonic.
func (c *ManualClock) Set(t time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.Before(c.now) {
		panic("leasefence: cannot move the clock backwards")
	}
	c.now = t
	return c.now
}
