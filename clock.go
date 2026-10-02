package streamjoin

import (
	"sync"
	"time"
)

// Clock abstracts time so the joiner can be driven deterministically in
// tests (ManualClock) or by the wall clock in production (RealClock).
type Clock interface {
	Now() time.Time
	// AfterFunc schedules f to run after d and returns a cancel function.
	AfterFunc(d time.Duration, f func()) (cancel func())
}

// RealClock implements Clock with the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) func() {
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

// ManualClock is a deterministic Clock for tests and demos. Timers fire
// synchronously, in deadline order, when Advance moves past their deadline.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers map[int]*manualTimer
}

type manualTimer struct {
	at        time.Time
	f         func()
	cancelled bool
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start, timers: make(map[int]*manualTimer)}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) AfterFunc(d time.Duration, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := c.seq
	at := c.now.Add(d)
	c.timers[id] = &manualTimer{at: at, f: f}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t, ok := c.timers[id]; ok {
			t.cancelled = true
			delete(c.timers, id)
		}
	}
}

// Advance moves the clock to t and fires every timer due at or before t,
// in deadline order. Timers scheduled by fired callbacks also run if due.
func (c *ManualClock) Advance(t time.Time) {
	for {
		c.mu.Lock()
		if t.Before(c.now) {
			c.mu.Unlock()
			return
		}
		c.now = t
		var due *manualTimer
		var dueID int
		for id, tm := range c.timers {
			if tm.cancelled || tm.at.After(t) {
				continue
			}
			if due == nil || tm.at.Before(due.at) {
				due, dueID = tm, id
			}
		}
		if due == nil {
			c.mu.Unlock()
			return
		}
		delete(c.timers, dueID)
		c.mu.Unlock()
		due.f()
	}
}

// PendingTimers reports how many timers are still scheduled (test aid).
func (c *ManualClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}
