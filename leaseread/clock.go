package leaseread

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can control lease expiry deterministically.
type Clock interface {
	Now() time.Time
}

// RealClock is the production clock backed by the monotonic system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// ManualClock is a test clock advanced explicitly by the caller.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
