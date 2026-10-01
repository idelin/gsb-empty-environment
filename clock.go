package joiner

import (
	"sync"
	"time"
)

// Clock abstracts time so the joiner can be tested deterministically.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a cancellable scheduled callback.
type Timer interface {
	// Stop reports whether the callback was prevented from running.
	Stop() bool
}

// RealClock implements Clock with the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// FakeClock is a manually advanced Clock for deterministic tests and demos.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers map[int]*fakeTimer
}

type fakeTimer struct {
	clock   *FakeClock
	id      int
	at      time.Time
	f       func()
	stopped bool
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, timers: make(map[int]*fakeTimer)}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, id: c.nextID, at: c.now.Add(d), f: f}
	c.nextID++
	c.timers[t.id] = t
	return t
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	if _, ok := t.clock.timers[t.id]; !ok {
		return false // already fired
	}
	t.stopped = true
	delete(t.clock.timers, t.id)
	return true
}

// Advance moves the clock forward by d, firing due timers in time order.
// Callbacks run without the clock lock held, so they may schedule new timers.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		var next *fakeTimer
		for _, t := range c.timers {
			if t.stopped || t.at.After(target) {
				continue
			}
			if next == nil || t.at.Before(next.at) ||
				(t.at.Equal(next.at) && t.id < next.id) {
				next = t
			}
		}
		if next == nil {
			break
		}
		c.now = next.at
		delete(c.timers, next.id)
		f := next.f
		c.mu.Unlock()
		f()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}
