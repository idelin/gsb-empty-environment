package netsec

import "time"

// Clock abstracts time so the state machine can be driven deterministically
// in tests with a fake clock.
type Clock interface {
	Now() time.Time
}

// SystemClock reports the wall-clock time.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// FakeClock is a manually controlled clock. It never advances on its own,
// which makes race-free ordering assertions possible in tests.
type FakeClock struct {
	now time.Time
}

// NewFakeClock returns a fake clock anchored at the given instant.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time { return c.now }

// Advance moves the clock forward by d and returns the new current time.
func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.now = c.now.Add(d)
	return c.now
}

// Set moves the clock to an explicit instant.
func (c *FakeClock) Set(t time.Time) { c.now = t }
