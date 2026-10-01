package lease

import (
	"sync"
	"time"
)

// Clock abstracts the time source so that tests and demos can inject
// clock drift and sudden clock steps (e.g. NTP corrections).
type Clock interface {
	Now() time.Time
}

// RealClock is the wall clock.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// SimClock is a manually controlled clock for deterministic simulation
// of a node's local clock, including drift and abrupt jumps.
type SimClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewSimClock creates a SimClock starting at start.
func NewSimClock(start time.Time) *SimClock {
	return &SimClock{now: start}
}

// Now returns the current simulated time.
func (c *SimClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d, modelling normally elapsed time.
func (c *SimClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Jump models a sudden clock step (e.g. an NTP correction of +1s).
// Mechanically identical to Advance; it exists to make the intent of
// fault injection explicit at call sites.
func (c *SimClock) Jump(d time.Duration) {
	c.Advance(d)
}
