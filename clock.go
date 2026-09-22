package aimd

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can inject a manual clock and make the
// adjustment sequence fully deterministic.
type Clock interface {
	Now() time.Time
}

// RealClock is the production clock backed by time.Now.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// ManualClock is a test clock that only moves when Advance is called.
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
