package netsec

import (
	"sort"
	"sync"
	"time"
)

// Scheduler schedules timeout callbacks. Production code uses NewAggregator's
// wall-clock scheduler; tests inject a FakeScheduler for deterministic,
// manually driven event ordering.
type Scheduler interface {
	// After schedules fn after d and returns a cancel func. Calling the cancel
	// func after the callback has run is a harmless no-op.
	After(d time.Duration, fn func()) (cancel func())
}

// wallScheduler drives callbacks on real goroutine timers.
type wallScheduler struct{}

func newWallScheduler() wallScheduler {
	return wallScheduler{}
}

func (wallScheduler) After(d time.Duration, fn func()) (cancel func()) {
	timer := time.AfterFunc(d, fn)
	return func() { timer.Stop() }
}

// fakeTimer is one pending FakeScheduler callback.
type fakeTimer struct {
	deadline time.Time
	seq      int
	fn       func()
	canceled bool
}

// FakeScheduler is a manually advanced clock. Timers only fire when Advance is
// called, and callbacks fire deterministically in (deadline, sequence) order,
// so races such as "last receipt vs cancellation/timeout" can be scripted.
type FakeScheduler struct {
	mu      sync.Mutex
	now     time.Time
	nextSeq int
	pending []*fakeTimer
}

// NewFakeScheduler returns a FakeScheduler anchored at the given time.
func NewFakeScheduler(start time.Time) *FakeScheduler {
	return &FakeScheduler{now: start}
}

// Now reports the fake clock's current time.
func (s *FakeScheduler) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// After queues a callback. It is safe concurrently, but tests normally drive
// the scheduler from a single goroutine.
func (s *FakeScheduler) After(d time.Duration, fn func()) (cancel func()) {
	s.mu.Lock()
	s.nextSeq++
	timer := &fakeTimer{deadline: s.now.Add(d), seq: s.nextSeq, fn: fn}
	s.pending = append(s.pending, timer)
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			timer.canceled = true
			s.mu.Unlock()
		})
	}
}

// Advance moves the clock by d and fires every due, non-canceled timer.
// Callbacks run without the scheduler lock held.
func (s *FakeScheduler) Advance(d time.Duration) {
	s.mu.Lock()
	s.now = s.now.Add(d)
	deadline := s.now

	due := make([]*fakeTimer, 0, len(s.pending))
	remaining := make([]*fakeTimer, 0, len(s.pending))
	for _, timer := range s.pending {
		if timer.canceled {
			continue
		}
		if !timer.deadline.After(deadline) {
			due = append(due, timer)
		} else {
			remaining = append(remaining, timer)
		}
	}
	s.pending = remaining
	s.mu.Unlock()

	sort.SliceStable(due, func(i, j int) bool {
		if due[i].deadline.Equal(due[j].deadline) {
			return due[i].seq < due[j].seq
		}
		return due[i].deadline.Before(due[j].deadline)
	})
	for _, timer := range due {
		timer.fn()
	}
}

// Pending reports how many live timers remain queued.
func (s *FakeScheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, timer := range s.pending {
		if !timer.canceled {
			count++
		}
	}
	return count
}
