package session

import "time"

// Clock is the time source consumed by the engine. Production deployments
// typically use SystemClock while tests inject a ManualClock.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the host wall clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// ManualClock is a deterministic clock advanced explicitly by tests. Time
// only moves forward: Advance and Set reject values before the current time.
type ManualClock struct {
	now time.Time
}

// NewManualClock creates a manual clock at the given instant.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now implements Clock.
func (c *ManualClock) Now() time.Time { return c.now }

// Advance moves the clock forward by d. Negative durations are ignored.
func (c *ManualClock) Advance(d time.Duration) {
	if d > 0 {
		c.now = c.now.Add(d)
	}
}

// Set moves the clock to t. Times earlier than the current reading are
// rejected to preserve a linear timeline.
func (c *ManualClock) Set(t time.Time) bool {
	if t.Before(c.now) {
		return false
	}
	c.now = t
	return true
}
