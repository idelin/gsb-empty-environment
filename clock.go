package netsec

import (
	"math"
	"sync"
	"time"
)

// Instant is a point on the host's monotonic timeline. It deliberately has no
// relationship to wall-clock time, so a token's deadline cannot be moved by
// changing the system clock.
type Instant int64

const (
	ZeroInstant Instant = 0
	MaxInstant  Instant = Instant(math.MaxInt64)
)

func (i Instant) Add(d time.Duration) Instant {
	return i + Instant(d)
}

func (i Instant) Sub(other Instant) time.Duration {
	return time.Duration(i - other)
}

func (i Instant) Before(other Instant) bool {
	return i < other
}

func (i Instant) After(other Instant) bool {
	return i > other
}

func (i Instant) String() string {
	return time.Duration(i).String()
}

// Clock is the host's monotonic clock. Implementations must never move
// backwards.
type Clock interface {
	Now() Instant
}

type SystemClock struct {
	base time.Time
}

func NewSystemClock() *SystemClock {
	return &SystemClock{base: time.Now()}
}

func (c *SystemClock) Now() Instant {
	return Instant(time.Since(c.base))
}

// ManualClock is useful for deterministic tests and for the audit harness.
// Advance only moves the clock forwards.
type ManualClock struct {
	mu  sync.Mutex
	now Instant
}

func NewManualClock(start Instant) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now += Instant(d)
	}
	return c.now
}

// Set is provided for fixtures that start at a non-zero instant. It refuses to
// move backwards.
func (c *ManualClock) Set(at Instant) Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at.After(c.now) {
		c.now = at
	}
	return c.now
}
