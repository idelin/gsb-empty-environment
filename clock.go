package dhcpsnoop

import "time"

// Clock is the time source used by the snooper. All time-based behavior
// (lease expiry, transaction timeout) reads from this interface so tests
// can drive packet sequences with a fully deterministic clock.
type Clock interface {
	Now() time.Time
}

// VirtualClock is a manually advanced Clock. Its zero value starts at the
// Unix epoch; use NewVirtualClock to pick another origin.
type VirtualClock struct {
	now time.Time
}

// NewVirtualClock creates a virtual clock positioned at start. A zero
// start defaults to the Unix epoch.
func NewVirtualClock(start time.Time) *VirtualClock {
	if start.IsZero() {
		start = time.Unix(0, 0).UTC()
	}
	return &VirtualClock{now: start}
}

// Now returns the current virtual time.
func (c *VirtualClock) Now() time.Time { return c.now }

// Advance moves the clock forward by d and returns the new time.
func (c *VirtualClock) Advance(d time.Duration) time.Time {
	c.now = c.now.Add(d)
	return c.now
}

// Set moves the clock to an absolute time.
func (c *VirtualClock) Set(t time.Time) { c.now = t }
