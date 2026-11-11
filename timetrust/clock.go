package timetrust

import "time"

// Clock is the only time source available to a Trust instance.
//
// Wall time is treated as advisory only: the device owner can move it
// forwards or backwards, so it must never widen a trusted interval. Wall
// time is accepted as an input for logging convenience but never used in a
// trust decision. Monotonic time measures elapsed time relative to an
// arbitrary epoch; the zero value means "no reading available yet".
type Clock interface {
	Now() (wall time.Time, monotonic time.Duration)
}

// SystemClock implements Clock using the host clock. time.Time returned by
// time.Now carries both wall and monotonic readings on Go's supported
// platforms; Sub exposes the monotonic component.
type SystemClock struct{}

// Now returns the current wall time and monotonic elapsed reading.
func (SystemClock) Now() (time.Time, time.Duration) {
	now := time.Now()
	wall := now.Truncate(0) // Truncate(0) strips the monotonic reading.
	return wall, now.Sub(wall)
}

// FakeClock is a fully controllable clock for tests. Monotonic readings only
// move when explicitly advanced, so wall time can be rolled backwards while
// monotonic time keeps going forward independently.
type FakeClock struct {
	wall       time.Time
	monotonic  time.Duration
	resetCount int
}

// NewFakeClock returns a FakeClock anchored at wall with a monotonic epoch of
// one second, deliberately non-zero so callers cannot confuse it with the
// "no reading" sentinel.
func NewFakeClock(wall time.Time) *FakeClock {
	return &FakeClock{wall: wall, monotonic: time.Second}
}

// Now reports the fake wall and monotonic readings.
func (c *FakeClock) Now() (time.Time, time.Duration) {
	return c.wall, c.monotonic
}

// SetWall moves the wall clock without touching the monotonic timer. It may
// move backwards, simulating a user turning the clock back.
func (c *FakeClock) SetWall(t time.Time) *FakeClock {
	c.wall = t
	return c
}

// Advance advances both the wall clock and the monotonic timer by d.
func (c *FakeClock) Advance(d time.Duration) *FakeClock {
	c.wall = c.wall.Add(d)
	c.monotonic += d
	return c
}

// AdvanceMonotonic moves only the monotonic timer, modelling elapsed time the
// wall clock does not reflect (for example while suspended).
func (c *FakeClock) AdvanceMonotonic(d time.Duration) *FakeClock {
	c.monotonic += d
	return c
}

// ResetMonotonic restarts the monotonic timer at a fresh epoch smaller than
// the previous reading, as happens after a reboot or a counter wrap.
func (c *FakeClock) ResetMonotonic() *FakeClock {
	c.resetCount++
	c.monotonic = time.Duration(c.resetCount) * time.Millisecond
	return c
}
