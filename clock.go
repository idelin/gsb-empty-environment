package netsec

import (
	"sync"
	"time"
)

// Clock is a trusted monotonic time source. Readings are nanoseconds from an
// arbitrary epoch and must never move backwards. The state machine also clamps
// backwards readings to its persisted high-water mark as a second line of
// defence.
type Clock interface {
	Now() int64
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() int64

func (f ClockFunc) Now() int64 {
	return f()
}

// SystemClock uses the monotonic component of time.Time for elapsed time while
// the process is running. The persisted high-water mark carried by Machine
// prevents a wall-clock rollback from shortening an observation across
// restarts.
type SystemClock struct {
	origin time.Time
	base   int64
}

func NewSystemClock() *SystemClock {
	now := time.Now()
	return &SystemClock{origin: now, base: now.UnixNano()}
}

func (c *SystemClock) Now() int64 {
	return c.base + int64(time.Since(c.origin))
}

// ManualClock is a deterministic clock for tests and for hosts that already
// have a trusted monotonic tick source.
type ManualClock struct {
	mu  sync.Mutex
	now int64
}

func NewManualClock(epoch time.Time) *ManualClock {
	return &ManualClock{now: epoch.UnixNano()}
}

func NewManualClockAt(tick int64) *ManualClock {
	return &ManualClock{now: tick}
}

func (c *ManualClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += int64(d)
}

// SetTick deliberately allows time to move backwards. It is useful for
// exercising rollback handling (and for a host simulator replaying a clock).
func (c *ManualClock) SetTick(tick int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = tick
}

func (c *ManualClock) Set(epoch time.Time) {
	c.SetTick(epoch.UnixNano())
}
