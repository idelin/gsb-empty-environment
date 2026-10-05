package batchwriter

import (
	"sync"
	"time"
)

// Clock abstracts time so the batcher can be driven by a virtual clock in tests.
type Clock interface {
	Now() time.Time
	// AfterFunc schedules f to run once after d has elapsed on this clock.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a handle to a scheduled callback.
type Timer interface {
	// Stop prevents the callback from firing. It reports whether the
	// callback was still pending.
	Stop() bool
}

// RealClock is the wall-clock implementation backed by the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return realTimer{t: time.AfterFunc(d, f)}
}

type realTimer struct{ t *time.Timer }

func (r realTimer) Stop() bool { return r.t.Stop() }

// FakeClock is a deterministic virtual clock. Time only moves when Advance
// is called. Timers fire synchronously, in (deadline, registration) order,
// so callbacks scheduled for the same tick run in FIFO order.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    uint64
	timers []*fakeTimer
}

type fakeTimer struct {
	clockMu *sync.Mutex
	at      time.Time
	seq     uint64
	f       func()
	stopped bool
	fired   bool
}

// NewFakeClock returns a FakeClock starting at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clockMu: &c.mu, at: c.now.Add(d), seq: c.seq, f: f}
	c.seq++
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d, synchronously running every timer
// whose deadline falls within the advanced window. Timers scheduled by
// callbacks are picked up by the same Advance if their deadline allows.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	deadline := c.now.Add(d)
	c.mu.Unlock()

	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if t.stopped || t.fired || t.at.After(deadline) {
				continue
			}
			if next == nil || t.at.Before(next.at) ||
				(t.at.Equal(next.at) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now = deadline
			c.timers = pruneTimers(c.timers)
			c.mu.Unlock()
			return
		}
		next.fired = true
		c.now = next.at
		f := next.f
		c.mu.Unlock()
		f()
	}
}

func pruneTimers(ts []*fakeTimer) []*fakeTimer {
	kept := ts[:0]
	for _, t := range ts {
		if !t.stopped && !t.fired {
			kept = append(kept, t)
		}
	}
	return kept
}

func (t *fakeTimer) Stop() bool {
	t.clockMu.Lock()
	defer t.clockMu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}
