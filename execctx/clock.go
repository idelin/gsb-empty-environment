package execctx

import "time"

// Clock abstracts time so deadline behavior stays deterministic in tests.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Timer allows canceling a scheduled deadline callback.
type Timer interface {
	Stop() bool
}

// wallClock uses the process clock and real timers.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

// FakeClock is a test clock whose timers fire when time is advanced.
type FakeClock interface {
	Clock
	Advance(d time.Duration)
}

// NewFakeClock returns a manually advanced Clock starting at t.
func NewFakeClock(t time.Time) FakeClock {
	return &fakeClock{now: t}
}

type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
	nextID int
}

type fakeTimer struct {
	id      int
	at      time.Time
	fn      func()
	clock   *fakeClock
	fired   bool
	stopped bool
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.nextID++
	t := &fakeTimer{
		id:    c.nextID,
		at:    c.now.Add(d),
		fn:    fn,
		clock: c,
	}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward and fires every due timer, in due order.
func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
	for {
		var due *fakeTimer
		idx := -1
		for i, t := range c.timers {
			if !t.fired && !t.stopped && !t.at.After(c.now) {
				if due == nil || t.at.Before(due.at) {
					due = t
					idx = i
				}
			}
		}
		if due == nil {
			return
		}
		due.fired = true
		c.timers = append(c.timers[:idx], c.timers[idx+1:]...)
		due.fn()
	}
}

func (t *fakeTimer) Stop() bool {
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	for i, x := range t.clock.timers {
		if x == t {
			t.clock.timers = append(t.clock.timers[:i], t.clock.timers[i+1:]...)
			break
		}
	}
	return true
}
