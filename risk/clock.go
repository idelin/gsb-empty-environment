package risk

import "sync"
import "time"

// Clock abstracts time so tests can drive the engine deterministically.
// Timestamps are expressed in Unix seconds.
type Clock interface {
	Now() int64
}

// RealClock uses wall-clock time.
type RealClock struct{}

func (RealClock) Now() int64 { return time.Now().Unix() }

// ManualClock is a test/demo clock controlled by the caller.
type ManualClock struct {
	mu  sync.Mutex
	now int64
}

func NewManualClock(start int64) *ManualClock { return &ManualClock{now: start} }

func (c *ManualClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Set(t int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *ManualClock) Advance(d int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
}
