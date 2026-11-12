package capnet

import (
	"sort"
	"sync"
	"time"
)

// epoch is the fixed, deterministic origin of every simulated clock.
var epoch = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    uint64
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: epoch}
}

func (c *fakeClock) nowTime() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// after schedules fn to run when the clock has advanced by at least d. Nothing
// fires until Advance is called, so zero-duration timers still wait for an
// explicit advance.
func (c *fakeClock) after(d time.Duration, fn func()) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &fakeTimer{clock: c, fireAt: c.now.Add(d), seq: c.seq, fn: fn, done: make(chan struct{})}
	c.timers = append(c.timers, t)
	return t
}

// stopAll invalidates every pending timer; used when a trial generation ends.
func (c *fakeClock) stopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.fired && !t.stopped {
			t.stopped = true
			close(t.done)
		}
	}
	c.gcLocked()
}

func (c *fakeClock) advance(d time.Duration) {
	for {
		c.mu.Lock()
		c.now = c.now.Add(d)
		d = 0
		var due []*fakeTimer
		for _, t := range c.timers {
			if !t.stopped && !t.fired && !t.fireAt.After(c.now) {
				due = append(due, t)
			}
		}
		sort.SliceStable(due, func(i, j int) bool {
			if due[i].fireAt.Equal(due[j].fireAt) {
				return due[i].seq < due[j].seq
			}
			return due[i].fireAt.Before(due[j].fireAt)
		})
		c.gcLocked()
		c.mu.Unlock()

		if len(due) == 0 {
			return
		}
		for _, t := range due {
			c.mu.Lock()
			if t.fired || t.stopped {
				c.mu.Unlock()
				continue
			}
			t.fired = true
			fn := t.fn
			done := t.done
			c.mu.Unlock()
			close(done)
			fn()
		}
	}
}

func (c *fakeClock) gcLocked() {
	kept := c.timers[:0]
	for _, t := range c.timers {
		if !t.fired && !t.stopped {
			kept = append(kept, t)
		}
	}
	c.timers = kept
}

type fakeTimer struct {
	clock   *fakeClock
	fireAt  time.Time
	seq     uint64
	fired   bool
	stopped bool
	fn      func()
	done    chan struct{}
}

func (t *fakeTimer) stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.fired && !t.stopped
	if active {
		t.stopped = true
		close(t.done)
	}
	return active
}
