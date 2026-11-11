package scheduler

import (
	"sync"
	"time"
)

// Clock is the time source used by the Scheduler. Production callers use the
// default real clock; tests inject a fake clock so minimum-delay timing can be
// advanced deterministically without real waiting.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is a minimal timer abstraction mirroring time.Timer.
type Timer interface {
	Chan() <-chan time.Time
	Stop() bool
}

// RealClock is the default Clock backed by the standard time package.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// NewTimer creates a timer backed by time.NewTimer.
func (RealClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) Chan() <-chan time.Time { return r.t.C }

func (r *realTimer) Stop() bool { return r.t.Stop() }

// FakeClock is a manually controlled Clock for tests. Timers never fire on
// their own; they fire only when the clock is Advanced past their deadline.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// NewFakeClock returns a FakeClock starting at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// pending reports how many timers are registered but not yet fired or
// stopped. It is a test helper.
func (c *FakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

// waitForTimers blocks until at least n pending timers are registered or the
// deadline passes. It is a test helper used to avoid racing Advance against
// timer registration.
func (c *FakeClock) waitForTimers(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.timers)
		c.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers) >= n
}

// NewTimer registers a timer that fires once the clock reaches now+d.
func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		fc:       c,
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d, firing every due timer once in
// deadline order. Timer notifications are buffered and delivered without
// blocking, so Advance never waits on a timer consumer.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	target := c.now.Add(d)
	c.now = target

	for {
		next := -1
		for i, t := range c.timers {
			if t.stopped || t.fired || t.deadline.After(target) {
				continue
			}
			if next == -1 || t.deadline.Before(c.timers[next].deadline) {
				next = i
			}
		}
		if next == -1 {
			return
		}

		t := c.timers[next]
		t.fired = true
		c.timers = append(c.timers[:next], c.timers[next+1:]...)
		select {
		case t.ch <- t.deadline:
		default:
		}
	}
}

type fakeTimer struct {
	fc       *FakeClock
	deadline time.Time
	ch       chan time.Time
	fired    bool
	stopped  bool
}

func (t *fakeTimer) Chan() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.fc.mu.Lock()
	defer t.fc.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}
