package txnsched

import (
	"sync"
	"time"
)

// Clock abstracts time so the scheduler can be driven by a virtual
// clock in tests and by the wall clock in production.
type Clock interface {
	Now() time.Time
	// AfterFunc schedules f to run after d elapses on the clock.
	// The returned Timer can cancel the callback before it fires.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer cancels a callback scheduled with Clock.AfterFunc.
type Timer interface {
	// Stop reports whether the callback was cancelled before firing.
	Stop() bool
}

// RealClock implements Clock with the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// VirtualClock is a manually advanced Clock for deterministic replay.
// Callbacks fire synchronously inside Advance, in deadline order.
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers []*virtualTimer
}

type virtualTimer struct {
	clock    *VirtualClock
	deadline time.Time
	seq      int
	fn       func()
	stopped  bool
}

// NewVirtualClock returns a VirtualClock starting at start.
func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start}
}

// Now implements Clock.
func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc implements Clock.
func (c *VirtualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &virtualTimer{clock: c, deadline: c.now.Add(d), seq: c.seq, fn: f}
	c.seq++
	c.timers = append(c.timers, t)
	return t
}

// Stop implements Timer.
func (t *virtualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

// Advance moves the clock forward by d, firing every timer whose
// deadline falls inside the advanced window, in deadline order.
// Timers scheduled by callbacks inside the window also fire.
func (c *VirtualClock) Advance(d time.Duration) {
	end := c.Now().Add(d)
	for {
		c.mu.Lock()
		var next *virtualTimer
		for _, t := range c.timers {
			if t.stopped || t.deadline.After(end) {
				continue
			}
			if next == nil || t.deadline.Before(next.deadline) ||
				(t.deadline.Equal(next.deadline) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now = end
			c.compactLocked()
			c.mu.Unlock()
			return
		}
		c.now = next.deadline
		next.stopped = true
		fn := next.fn
		c.mu.Unlock()
		fn()
	}
}

func (c *VirtualClock) compactLocked() {
	kept := c.timers[:0]
	for _, t := range c.timers {
		if !t.stopped {
			kept = append(kept, t)
		}
	}
	c.timers = kept
}
