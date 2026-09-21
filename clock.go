package scheduler

import (
	"sync"
	"time"
)

// Timer abstracts time.Timer so tests can inject a fake clock.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock provides the current time and creates timers. Tests inject a
// FakeClock; production code uses RealClock.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// RealClock implements Clock with the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }

func (r *realTimer) Stop() bool { return r.t.Stop() }

// FakeClock is a manually advanced Clock for tests. All methods are safe for
// concurrent use.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, timers: make(map[*fakeTimer]struct{})}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	if d <= 0 {
		t.ch <- c.now
		return t
	}
	c.timers[t] = struct{}{}
	return t
}

// Advance moves the clock forward by d and fires every expired timer.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.fireExpiredLocked()
}

// SetNow moves the clock to an absolute time, forwards or backwards. Moving
// backwards never fires timers; combined with the scheduler's monotonic
// round tracking, a rollback can never re-run a finished round.
func (c *FakeClock) SetNow(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	c.fireExpiredLocked()
}

func (c *FakeClock) fireExpiredLocked() {
	for t := range c.timers {
		if !t.deadline.After(c.now) {
			delete(c.timers, t)
			t.ch <- c.now
		}
	}
}

// PendingTimers reports how many timers are still armed. Tests use it to
// synchronize with the scheduler loop and to verify cleanup on Stop.
func (c *FakeClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type fakeTimer struct {
	clock    *FakeClock
	ch       chan time.Time
	deadline time.Time
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if _, ok := t.clock.timers[t]; ok {
		delete(t.clock.timers, t)
		return true
	}
	return false
}
