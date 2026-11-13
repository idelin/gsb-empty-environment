package netsec

import (
	"sync"
	"time"
)

// Clock is the time source used by a Ledger.  Production code normally uses
// time.Now directly; tests and replay tools can use VirtualClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// VirtualClock is a manually advanced clock.  It is safe for concurrent use.
// Advancing time does not itself mutate a Ledger; Ledger lazily expires stale
// routes at the start of every operation, and Ledger.Advance also prunes
// immediately when it owns a VirtualClock.
type VirtualClock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewVirtualClock returns a clock starting at the Unix epoch (UTC).
func NewVirtualClock() *VirtualClock {
	return NewVirtualClockAt(time.Unix(0, 0).UTC())
}

// NewVirtualClockAt returns a clock starting at t.
func NewVirtualClockAt(t time.Time) *VirtualClock {
	return &VirtualClock{now: t}
}

// NewFakeClockAt is an alias for NewVirtualClockAt.
func NewFakeClockAt(t time.Time) *VirtualClock { return NewVirtualClockAt(t) }

// NewFakeClock is an alias for NewVirtualClock.
func NewFakeClock() *VirtualClock { return NewVirtualClock() }

// NewManualClock is an alias for NewVirtualClock.
func NewManualClock() *VirtualClock { return NewVirtualClock() }

func (c *VirtualClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Set sets the wall clock.  Time is allowed to move backwards for replay, but
// a Ledger never resurrects a route that has already been withdrawn.
func (c *VirtualClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// Advance moves the clock forward by d and returns the new time.  A negative
// duration is ignored.
func (c *VirtualClock) Advance(d time.Duration) time.Time {
	if d < 0 {
		d = 0
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	return now
}

// Add is an alias for Advance.
func (c *VirtualClock) Add(d time.Duration) time.Time { return c.Advance(d) }

// Tick is an alias for Advance.
func (c *VirtualClock) Tick(d time.Duration) time.Time { return c.Advance(d) }

type advancingClock interface {
	Clock
	Advance(time.Duration) time.Time
}

func clockFrom(v any) Clock {
	switch c := v.(type) {
	case nil:
		return realClock{}
	case Clock:
		return c
	case func() time.Time:
		return clockFunc(c)
	default:
		return realClock{}
	}
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }
