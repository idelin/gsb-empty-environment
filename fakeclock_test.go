package authbackoff

import (
	"sync"
	"time"
)

// fakeClock is a manually driven Clock. Time only changes when a test calls
// Advance or Set, which keeps scheduling fully deterministic.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set jumps the clock to an arbitrary instant, including an earlier one, to
// simulate wall-clock rollback.
func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// hookClock wraps another Clock and runs hook on every Now call. Tests use
// the hook as a barrier to interleave goroutines at a precise moment.
type hookClock struct {
	inner Clock
	hook  func()
}

func (c *hookClock) Now() time.Time {
	if c.hook != nil {
		c.hook()
	}
	return c.inner.Now()
}

// barrier is a reusable start gate: every waiter blocks until release is
// called once.
type barrier struct {
	once sync.Once
	ch   chan struct{}
}

func newBarrier() *barrier {
	return &barrier{ch: make(chan struct{})}
}

func (b *barrier) wait()    { <-b.ch }
func (b *barrier) release() { b.once.Do(func() { close(b.ch) }) }
