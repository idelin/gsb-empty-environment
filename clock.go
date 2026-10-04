package batcher

import (
	"sync"
	"time"
)

// Timer is a single-shot countdown that fires once unless stopped.
type Timer interface {
	// Stop prevents the timer from firing. It reports whether the timer
	// was still pending (i.e. Stop won the race against firing).
	Stop() bool
}

// Clock abstracts time so tests can drive flushes deterministically.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// RealClock implements Clock with the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) Stop() bool { return r.t.Stop() }

// ManualClock is a virtual-time Clock for tests. Timers fire
// synchronously, in deadline order, inside Advance.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &manualTimer{at: c.now.Add(d), f: f, clock: c}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves virtual time forward by d, firing every timer whose
// deadline falls within the interval, earliest first. A timer callback
// may schedule new timers; those fire within the same Advance if their
// deadline is still inside the interval.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		var next *manualTimer
		for _, t := range c.timers {
			if !t.stopped && !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			break
		}
		c.now = next.at
		next.stopped = true
		f := next.f
		c.mu.Unlock()
		f()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}

type manualTimer struct {
	at      time.Time
	f       func()
	stopped bool
	clock   *ManualClock
}

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}
