package jobqueue

import "time"

// Clock abstracts time so tests can drive the queue with a virtual clock.
type Clock interface {
	Now() time.Time
}

// FakeClock is a manually advanced virtual clock for tests.
type FakeClock struct {
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time { return c.now }

// Advance moves the virtual clock forward by d.
func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
