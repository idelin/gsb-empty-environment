package retry

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is the minimal time abstraction used by all runtime components.
type Clock interface {
	Now() time.Time
}

// Scheduler extends Clock with timer creation. A timer fires through its
// channel after d; CtxTimer fires when the deadline is reached or the
// context is cancelled, whichever happens first.
type Scheduler interface {
	Clock
	After(d time.Duration) <-chan time.Time
	CtxTimer(ctx context.Context, d time.Duration) <-chan time.Time
}

// RealScheduler is the production scheduler backed by the standard library.
type RealScheduler struct{}

// NewRealScheduler returns a wall-clock scheduler.
func NewRealScheduler() RealScheduler { return RealScheduler{} }

// Now returns the current wall-clock time.
func (RealScheduler) Now() time.Time { return time.Now() }

// After returns a channel that receives the current time after d.
func (RealScheduler) After(d time.Duration) <-chan time.Time { return time.After(d) }

// CtxTimer returns a channel that fires after d or when ctx is done.
func (RealScheduler) CtxTimer(ctx context.Context, d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- time.Now()
		return ch
	}
	t := time.NewTimer(d)
	go func() {
		select {
		case at := <-t.C:
			ch <- at
		case <-ctx.Done():
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			ch <- time.Now()
		}
	}()
	return ch
}

// simEvent is a pending timer inside SimScheduler.
type simEvent struct {
	at   int64
	id   int64
	fire chan time.Time
}

// SimScheduler is a fully deterministic virtual clock. Time only advances
// when RunFor / Advance is called; it is safe for single-goroutine
// simulation loops (the steady package DES) and for focused unit tests.
type SimScheduler struct {
	mu     sync.Mutex
	now    int64 // nanoseconds since the virtual epoch
	nextID int64
	events []simEvent
}

// NewSimScheduler creates a virtual clock starting at the Unix epoch.
func NewSimScheduler() *SimScheduler {
	return &SimScheduler{}
}

// Now returns the virtual current time.
func (s *SimScheduler) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Unix(0, s.now)
}

// After schedules a virtual timer.
func (s *SimScheduler) After(d time.Duration) <-chan time.Time {
	return s.CtxTimer(context.Background(), d)
}

// CtxTimer schedules a virtual timer bound to ctx.
func (s *SimScheduler) CtxTimer(ctx context.Context, d time.Duration) <-chan time.Time {
	// Cancellation is cooperative: callers select on ctx.Done() themselves.
	// We keep the context parameter only to satisfy the Scheduler interface.
	ch := make(chan time.Time, 1)
	s.mu.Lock()
	at := s.now + int64(d)
	if d <= 0 {
		at = s.now
	}
	s.nextID++
	id := s.nextID
	s.events = append(s.events, simEvent{at: at, id: id, fire: ch})
	s.mu.Unlock()
	return ch
}

// Pending reports how many virtual timers are queued.
func (s *SimScheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// Advance moves the virtual clock to t, firing every timer due by t in
// chronological order. Timers scheduled at the same instant fire in the
// order they were created.
func (s *SimScheduler) Advance(t time.Time) {
	for {
	sched:
		s.mu.Lock()
		if len(s.events) == 0 {
			s.now = t.UnixNano()
			s.mu.Unlock()
			return
		}
		sort.Slice(s.events, func(i, j int) bool {
			if s.events[i].at != s.events[j].at {
				return s.events[i].at < s.events[j].at
			}
			return s.events[i].id < s.events[j].id
		})
		ev := s.events[0]
		if ev.at > t.UnixNano() {
			s.now = t.UnixNano()
			s.mu.Unlock()
			return
		}
		s.events = s.events[1:]
		s.now = ev.at
		s.mu.Unlock()
		ev.fire <- time.Unix(0, ev.at)
		goto sched
	}
}

// RunFor advances the virtual clock by d.
func (s *SimScheduler) RunFor(d time.Duration) {
	s.mu.Lock()
	target := s.now + int64(d)
	s.mu.Unlock()
	s.Advance(time.Unix(0, target))
}
