package fetchquota

import (
	"sort"
	"sync"
	"time"
)

// Clock is the time abstraction used by the Scheduler. Production callers use
// RealClock; deterministic tests use FakeClock and advance it manually.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer mirrors the subset of time.Timer that the Scheduler relies on.
type Timer interface {
	// C returns the channel on which the firing time is delivered.
	C() <-chan time.Time
	// Stop prevents the timer from firing. It reports whether the call stopped
	// the timer before it fired.
	Stop() bool
	// Reset changes the timer to expire after duration d.
	Reset(d time.Duration) bool
}

// RealClock is backed by the standard time package.
type RealClock struct{}

// NewRealClock returns a Clock driven by the wall clock.
func NewRealClock() RealClock { return RealClock{} }

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// fakeTimer is a timer controlled by FakeClock.
type fakeTimer struct {
	fc      *FakeClock
	c       chan time.Time
	when    time.Time
	stopped bool
	fired   bool
	stop    chan struct{}
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.fc.mu.Lock()
	active := !t.stopped && !t.fired
	alreadyStopped := t.stopped
	t.stopped = true
	delete(t.fc.timers, t)
	stop := t.stop
	t.fc.mu.Unlock()
	if !alreadyStopped {
		close(stop)
	}
	return active
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.fc.mu.Lock()
	active := !t.stopped && !t.fired
	oldStop := t.stop
	t.stopped = false
	t.fired = false
	t.when = t.fc.now.Add(d)
	t.fc.timers[t] = struct{}{}
	t.stop = make(chan struct{})
	t.fc.mu.Unlock()
	close(oldStop)
	return active
}

// FakeClock is a manually driven Clock for deterministic tests. Timers only
// fire while Advance is called, so concurrent goroutines can never observe
// time passing on their own.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
	// onAdvance, if set, runs after each Advance with no clock lock held.
	// The scheduler uses it to broadcast its condition when time moves.
	onAdvance func()
}

// NewFakeClock returns a FakeClock anchored at a fixed base instant.
func NewFakeClock() *FakeClock {
	return &FakeClock{
		now:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		timers: make(map[*fakeTimer]struct{}),
	}
}

// Now returns the fake current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer schedules a timer at Now()+d. A non-positive d expires at the
// current instant and fires on the next Advance.
func (f *FakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		fc:   f,
		c:    make(chan time.Time, 1),
		when: f.now.Add(d),
		stop: make(chan struct{}),
	}
	if t.when.Before(f.now) {
		t.when = f.now
	}
	f.timers[t] = struct{}{}
	return t
}

// Advance moves the clock forward by d and, before returning, fires every
// timer that becomes due, in chronological order. Timers created by firing
// goroutines and due within the advanced window fire in the same call.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	hook := f.onAdvance
	for {
		due := f.dueTimerLocked()
		if due == nil {
			f.mu.Unlock()
			if hook != nil {
				hook()
			}
			return
		}
		delete(f.timers, due)
		due.fired = true
		when := due.when
		stop := due.stop
		// Deliver without holding the clock lock so blocked receivers run.
		f.mu.Unlock()
		select {
		case due.c <- when:
		case <-stop:
			// The timer was stopped concurrently after we selected it.
		}
		f.mu.Lock()
	}
}

// dueTimerLocked returns the earliest pending timer not after Now, or nil.
func (f *FakeClock) dueTimerLocked() *fakeTimer {
	pending := make([]*fakeTimer, 0, len(f.timers))
	for t := range f.timers {
		if !t.when.After(f.now) {
			pending = append(pending, t)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].when.Before(pending[j].when) })
	return pending[0]
}

// pendingTimers reports how many timers are still scheduled (test helper).
func (f *FakeClock) pendingTimers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}
