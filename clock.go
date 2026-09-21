package pubsub

import (
	"sort"
	"sync"
	"time"
)

// Clock abstracts time so retries can be tested deterministically without
// real sleeping.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of *time.Timer used by this package.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// WallClock uses the real, wall-clock time.
type WallClock struct{}

// NewTimer returns a timer backed by time.NewTimer.
func (WallClock) NewTimer(d time.Duration) Timer {
	return wallTimer{time.NewTimer(d)}
}

// Now returns the current wall-clock time.
func (WallClock) Now() time.Time { return time.Now() }

type wallTimer struct{ t *time.Timer }

func (w wallTimer) C() <-chan time.Time { return w.t.C }
func (w wallTimer) Stop() bool          { return w.t.Stop() }
func (w wallTimer) Reset(d time.Duration) bool {
	return w.t.Reset(d)
}

// FakeClock is a manually driven Clock for tests. Timers never fire on their
// own; callers move time forward with Advance.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int64
	timers map[int64]*fakeTimer
	nextID int64
}

// NewFakeClock creates a fake clock at the given starting time.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, timers: make(map[int64]*fakeTimer)}
}

// Now reports the fake current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer schedules a fake timer.
func (f *FakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	t := &fakeTimer{
		id:       f.nextID,
		clock:    f,
		deadline: f.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	f.timers[t.id] = t
	return t
}

func (f *FakeClock) remove(id int64) { delete(f.timers, id) }

// Advance moves the clock forward by d, firing all timers whose deadline is
// at or before the new time in deadline order.
func (f *FakeClock) Advance(d time.Duration) int {
	f.mu.Lock()
	target := f.now.Add(d)
	var due []*fakeTimer
	for _, t := range f.timers {
		if !t.deadline.After(target) {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].deadline.Equal(due[j].deadline) {
			return due[i].id < due[j].id
		}
		return due[i].deadline.Before(due[j].deadline)
	})
	for _, t := range due {
		delete(f.timers, t.id)
	}
	f.now = target
	f.mu.Unlock()

	for _, t := range due {
		t.fire(target)
	}
	return len(due)
}

// Pending reports how many timers are currently scheduled.
func (f *FakeClock) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

type fakeTimer struct {
	id       int64
	clock    *FakeClock
	deadline time.Time
	ch       chan time.Time
	mu       sync.Mutex
	stopped  bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	t.clock.mu.Lock()
	_, active := t.clock.timers[t.id]
	delete(t.clock.timers, t.id)
	t.clock.mu.Unlock()
	return active
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	wasActive := !t.stopped
	t.stopped = false
	t.clock.mu.Lock()
	_, present := t.clock.timers[t.id]
	t.deadline = t.clock.now.Add(d)
	if !present {
		t.clock.timers[t.id] = t
	}
	select {
	case <-t.ch:
	default:
	}
	t.clock.mu.Unlock()
	t.mu.Unlock()
	return wasActive && present
}

func (t *fakeTimer) fire(at time.Time) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	t.mu.Unlock()
	select {
	case t.ch <- at:
	default:
	}
}
