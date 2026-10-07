package queue

import "time"

// Clock abstracts time so tests can drive leases with a virtual clock.
type Clock interface {
	Now() time.Time
}

// ManualClock is a virtual clock advanced explicitly by tests.
type ManualClock struct {
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time { return c.now }

func (c *ManualClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
