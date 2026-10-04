// Package statewait provides a state store where goroutines wait on
// arbitrary predicates. Updaters only replace the state; they never need
// to know which predicates waiters registered.
//
// Correctness guarantees:
//
//   - Check-and-register is atomic: Wait evaluates the predicate and, if
//     unsatisfied, registers the waiter while holding the same lock that
//     Update holds when committing a new state. A state change can never
//     slip between the check and the registration, so no waiter sleeps
//     forever on a state that already satisfies it.
//   - Exactly-once return: notification and cancellation are serialized
//     by the same lock. A wait resolves either with a satisfying state or
//     with the context error, never both, and Wait returns exactly once.
//   - Verifiable visibility: every committed state is appended to an
//     event history with a monotonically increasing version. Every
//     successful Wait reports the version it observed, so callers can
//     verify the result against a real, visible state.
package statewait

import (
	"context"
	"sync"
)

// Event is one committed state transition.
type Event[T any] struct {
	Version uint64
	State   T
}

// Result is the outcome of a successful Wait: the observed state and the
// version of the event that produced it.
type Result[T any] struct {
	State   T
	Version uint64
}

// waiter is a registered predicate. ch has capacity 1 and receives exactly
// one send, performed under Store.mu at the moment the waiter is removed
// from the registry.
type waiter[T any] struct {
	pred func(T) bool
	ch   chan Result[T]
}

// Store holds a versioned state and a registry of waiting predicates.
type Store[T any] struct {
	mu      sync.Mutex
	state   T
	version uint64
	waiters map[*waiter[T]]struct{}
	history []Event[T]
}

// New returns a Store initialized to initial (recorded as version 0).
func New[T any](initial T) *Store[T] {
	s := &Store[T]{
		state:   initial,
		waiters: make(map[*waiter[T]]struct{}),
	}
	s.history = append(s.history, Event[T]{Version: 0, State: initial})
	return s
}

// Get returns the current state and its version.
func (s *Store[T]) Get() Result[T] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Result[T]{State: s.state, Version: s.version}
}

// History returns a copy of the full event history, starting with the
// initial state at version 0.
func (s *Store[T]) History() []Event[T] {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event[T], len(s.history))
	copy(out, s.history)
	return out
}

// PendingWaiters reports how many waiters are currently registered.
func (s *Store[T]) PendingWaiters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiters)
}

// Update replaces the state with fn(current), commits it as a new history
// event, and wakes every waiter whose predicate holds for the new state.
//
// fn and the waiters' predicates run under the store lock: they must be
// fast, must not block, and must not call back into the Store.
func (s *Store[T]) Update(fn func(T) T) Result[T] {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = fn(s.state)
	s.version++
	res := Result[T]{State: s.state, Version: s.version}
	s.history = append(s.history, Event[T]{Version: s.version, State: s.state})

	for w := range s.waiters {
		if w.pred(s.state) {
			// Removal and the single buffered send happen atomically
			// under the lock, so each waiter is notified exactly once.
			delete(s.waiters, w)
			w.ch <- res
		}
	}
	return res
}

// Wait blocks until pred holds for some committed state or ctx is done.
//
// If pred already holds for the current state, Wait returns it immediately.
// Otherwise the waiter is registered atomically with the check, so any
// concurrent Update is guaranteed to re-evaluate the predicate.
//
// On success Wait returns the satisfying state and its version. If ctx is
// canceled first, Wait returns ctx.Err(). When cancellation and
// satisfaction race, exactly one outcome is returned.
//
// pred runs under the store lock: it must be fast, must not block, and
// must not call back into the Store.
func (s *Store[T]) Wait(ctx context.Context, pred func(T) bool) (Result[T], error) {
	s.mu.Lock()
	if pred(s.state) {
		res := Result[T]{State: s.state, Version: s.version}
		s.mu.Unlock()
		return res, nil
	}
	w := &waiter[T]{pred: pred, ch: make(chan Result[T], 1)}
	s.waiters[w] = struct{}{}
	s.mu.Unlock()

	select {
	case res := <-w.ch:
		return res, nil
	case <-ctx.Done():
		s.mu.Lock()
		if _, ok := s.waiters[w]; ok {
			// Cancellation wins: the waiter was never notified.
			delete(s.waiters, w)
			s.mu.Unlock()
			var zero Result[T]
			return zero, ctx.Err()
		}
		// Notification already won: the result was sent under the lock
		// before the waiter was removed, so it is buffered in w.ch.
		s.mu.Unlock()
		res := <-w.ch
		return res, nil
	}
}
