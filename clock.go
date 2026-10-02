package leasecoord

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can advance it deterministically.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a test clock that only moves when Advance is called.
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

func NewManualClock(start time.Time) *ManualClock { return &ManualClock{t: start} }

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
