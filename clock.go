package pool

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can control it deterministically.
type Clock interface {
	// After returns a channel that receives the current time once
	// duration d has elapsed.
	After(d time.Duration) <-chan time.Time
}

// realClock is the production Clock backed by the time package.
type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// FakeClock is a manually advanced Clock for deterministic tests.
// Timers fire only when Advance moves the clock past their deadline.
type FakeClock struct {
	mu     sync.Mutex
	cond   *sync.Cond
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

// NewFakeClock returns a FakeClock starting at the zero time.
func NewFakeClock() *FakeClock {
	c := &FakeClock{now: time.Unix(0, 0)}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// After implements Clock.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- c.now
		return t.ch
	}
	c.timers = append(c.timers, t)
	c.cond.Broadcast()
	return t.ch
}

// Advance moves the clock forward by d and fires every timer whose
// deadline has been reached. Fired timers are delivered on buffered
// channels, so Advance never blocks.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- c.now
		} else {
			remaining = append(remaining, t)
		}
	}
	c.timers = remaining
}

// WaitTimers blocks until at least n timers are pending. It is a test
// aid that removes the race between timer registration and Advance.
func (c *FakeClock) WaitTimers(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.timers) < n {
		c.cond.Wait()
	}
}
