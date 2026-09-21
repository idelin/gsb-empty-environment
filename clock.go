package tasklock

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can inject a manual clock and control
// when acquire timeouts fire.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production clock backed by the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ManualClock is a test-injected clock. Time only moves when Advance is
// called, which fires any timers that came due.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
}

type manualTimer struct {
	at time.Time
	ch chan time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, &manualTimer{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward and fires every timer that came due.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	fired := make([]*manualTimer, 0, len(c.timers))
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at.After(c.now) {
			kept = append(kept, tm)
		} else {
			fired = append(fired, tm)
		}
	}
	c.timers = kept
	now := c.now
	c.mu.Unlock()
	for _, tm := range fired {
		tm.ch <- now
	}
}
