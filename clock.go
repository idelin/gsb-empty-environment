package pool

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can drive timeouts deterministically.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ManualClock is a controllable Clock for tests: time only moves when
// Advance is called, firing any timers whose deadline has been reached.
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

// Advance moves the clock forward and fires all timers due by the new time.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var fired []*manualTimer
	remain := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(c.now) {
			fired = append(fired, tm)
		} else {
			remain = append(remain, tm)
		}
	}
	c.timers = remain
	now := c.now
	c.mu.Unlock()
	for _, tm := range fired {
		tm.ch <- now
	}
}

// Timers reports how many timers are pending; used by tests to synchronize.
func (c *ManualClock) Timers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}
