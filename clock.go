package devsched

import (
	"sync"
	"time"
)

// Clock abstracts time so the scheduler can be driven by a virtual clock
// in tests and by the system clock in production.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Timer is a scheduled callback that can be cancelled.
type Timer interface {
	// Stop reports whether the callback was cancelled before it fired.
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

// RealClock returns a Clock backed by the time package.
func RealClock() Clock { return realClock{} }

// VirtualClock is a manually advanced Clock for deterministic tests.
// Timers only fire when Advance or AdvanceTo is called.
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers map[int]*virtualTimer
}

type virtualTimer struct {
	clock    *VirtualClock
	id       int
	deadline time.Time
	fn       func()
	fired    bool
	stopped  bool
}

// NewVirtualClock returns a VirtualClock starting at start.
func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start, timers: make(map[int]*virtualTimer)}
}

// Now returns the virtual current time.
func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc registers fn to run when the virtual clock reaches now+d.
func (c *VirtualClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	t := &virtualTimer{clock: c, id: c.nextID, deadline: c.now.Add(d), fn: fn}
	c.timers[c.nextID] = t
	return t
}

// Advance moves the clock forward by d, firing due timers in deadline order.
func (c *VirtualClock) Advance(d time.Duration) {
	c.AdvanceTo(c.Now().Add(d))
}

// AdvanceTo moves the clock to target, firing due timers in deadline order.
// Callbacks run outside the clock lock and may register new timers.
func (c *VirtualClock) AdvanceTo(target time.Time) {
	for {
		c.mu.Lock()
		var earliest *virtualTimer
		earliestID := 0
		for id, t := range c.timers {
			if earliest == nil || t.deadline.Before(earliest.deadline) {
				earliest, earliestID = t, id
			}
		}
		if earliest == nil || earliest.deadline.After(target) {
			if c.now.Before(target) {
				c.now = target
			}
			c.mu.Unlock()
			return
		}
		c.now = earliest.deadline
		delete(c.timers, earliestID)
		earliest.fired = true
		fn := earliest.fn
		c.mu.Unlock()
		fn()
	}
}

func (t *virtualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t.id)
	return true
}
