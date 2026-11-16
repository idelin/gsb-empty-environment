package netsec

import (
	"sync"
	"time"
)

// Clock is the caller-supplied time source used for timing equalisation and
// the sliding failure window. Sleep is part of the interface so tests can
// simulate response latency without waiting on the wall clock.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// SystemClock is the default real-time clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits for the requested duration.
func (SystemClock) Sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// ManualClock is a deterministic Clock for firmware tests. Sleep advances the
// clock and records the requested duration.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

// NewManualClock creates a manually advanced clock. A zero start time is
// replaced with the Unix epoch in UTC.
func NewManualClock(start time.Time) *ManualClock {
	if start.IsZero() {
		start = time.Unix(0, 0).UTC()
	}
	return &ManualClock{now: start}
}

// Now returns the clock's current time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances the clock by d and records the duration.
func (c *ManualClock) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.sleeps = append(c.sleeps, d)
}

// Advance moves the clock forward without simulating a sleep.
func (c *ManualClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeps returns a copy of all recorded sleep durations.
func (c *ManualClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.sleeps))
	copy(out, c.sleeps)
	return out
}

// ResetSleeps clears the recorded sleep durations.
func (c *ManualClock) ResetSleeps() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = nil
}
