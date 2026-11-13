package netsec

import (
	"sync"
	"time"
)

// Clock abstracts time so callers can simulate response-time buckets
// and throttling windows deterministically.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// RealClock returns a Clock backed by the wall clock.
func RealClock() Clock { return realClock{} }

// ManualClock is a caller-driven Clock for simulation and testing.
// Sleep advances the clock instead of blocking.
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

func (c *ManualClock) Sleep(d time.Duration) { c.Advance(d) }

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
