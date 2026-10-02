package swim

import (
	"sync"
	"time"
)

// Timer is a single-shot countdown that fires on C.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock abstracts time so tests can drive it deterministically.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// RealClock implements Clock using the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool          { return r.t.Stop() }

// ManualClock is a deterministic Clock for tests: time only moves
// when Advance is called, firing every timer that comes due.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*manualTimer]struct{}
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start, timers: make(map[*manualTimer]struct{})}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &manualTimer{clock: c, at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.stopped = true
		t.ch <- c.now
		return t
	}
	c.timers[t] = struct{}{}
	return t
}

// Advance moves the clock forward by d, firing due timers in
// chronological order. Timers created while advancing are also
// fired if they come due before the new time.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := c.now.Add(d)
	for {
		var next *manualTimer
		for t := range c.timers {
			if !t.at.After(end) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			break
		}
		delete(c.timers, next)
		next.stopped = true
		c.now = next.at
		next.ch <- next.at
	}
	c.now = end
}

type manualTimer struct {
	clock   *ManualClock
	at      time.Time
	ch      chan time.Time
	stopped bool
}

func (t *manualTimer) C() <-chan time.Time { return t.ch }

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}
