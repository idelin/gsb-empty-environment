package proxy

import (
	"sync"
	"time"
)

// Clock abstracts time so cache expiry and revalidation can be driven
// deterministically in tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// SystemClock uses the wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time                         { return time.Now() }
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type fakeTimer struct {
	at    time.Time
	ch    chan time.Time
	fired bool
}

// FakeClock is a manually advanced clock. Timers registered via After fire
// when Advance reaches their deadline.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// NewFakeClock returns a FakeClock frozen at t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the fake current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After registers a timer that fires after d fake time.
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &fakeTimer{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.timers = append(f.timers, timer)
	return timer.ch
}

// Advance moves the clock forward and fires every due timer.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var due []*fakeTimer
	remaining := f.timers[:0]
	for _, timer := range f.timers {
		if !timer.fired && !timer.at.After(f.now) {
			timer.fired = true
			due = append(due, timer)
		} else if !timer.fired {
			remaining = append(remaining, timer)
		}
	}
	f.timers = remaining
	f.mu.Unlock()

	for _, timer := range due {
		select {
		case timer.ch <- timer.at:
		default:
		}
	}
}
