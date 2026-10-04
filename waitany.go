// Package waitany waits for the first of several one-shot events to fire,
// then releases every other registration.
//
// Semantics:
//   - Events are subscribed in input order. An event that has already fired
//     delivers its callback synchronously during Subscribe, so events that
//     complete during registration are never missed.
//   - The first callback to take effect wins. Events that complete at the
//     same time (e.g. both already fired before registration) are resolved
//     by input order: the lower index wins.
//   - Once a winner is chosen, all subscriptions are unsubscribed and the
//     winner's index is returned. The result is delivered exactly once.
//   - If the context is cancelled before any event takes effect, every
//     subscription is released and the context error is returned. An event
//     that completed before the cancellation was observed is never
//     swallowed: it wins over the cancellation.
package waitany

import (
	"context"
	"errors"
	"sync"
)

// ErrNoEvents is returned by WaitAny when called without any events.
var ErrNoEvents = errors.New("waitany: no events")

// Subscription is a handle to a registered interest in an Event.
type Subscription interface {
	// Unsubscribe removes the registration. It must be idempotent and must
	// guarantee the callback is not invoked after Unsubscribe returns
	// (a callback already in flight may still finish).
	Unsubscribe()
}

// Event is a one-shot event source.
type Event interface {
	// Subscribe registers cb to be invoked at most once, when the event
	// fires. If the event has already fired, cb must be invoked
	// synchronously before Subscribe returns. The returned Subscription
	// may be nil when there is nothing to release.
	Subscribe(cb func()) Subscription
}

// WaitAny registers a callback on every event and blocks until one of them
// fires or ctx is cancelled. It returns the index of the winning event, or
// -1 and the context error if the wait was cancelled first.
func WaitAny(ctx context.Context, events ...Event) (int, error) {
	if len(events) == 0 {
		return -1, ErrNoEvents
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}

	var mu sync.Mutex
	winner := -1
	fired := make(chan struct{})
	var subs []Subscription
	unsubscribeAll := func() {
		for _, sub := range subs {
			if sub != nil {
				sub.Unsubscribe()
			}
		}
	}

	// Registration phase: subscribe sequentially so that events which
	// completed before or during registration take effect in input order.
	for i, ev := range events {
		mu.Lock()
		done := winner != -1
		mu.Unlock()
		if done {
			break
		}
		// A completed event beats a cancellation observed at the same
		// time, so the winner is checked before the context.
		if err := ctx.Err(); err != nil {
			unsubscribeAll()
			return -1, err
		}
		idx := i
		sub := ev.Subscribe(func() {
			mu.Lock()
			defer mu.Unlock()
			if winner == -1 {
				winner = idx
				close(fired)
			}
		})
		if sub != nil {
			subs = append(subs, sub)
		}
	}

	// Waiting phase: only reached when no event fired during registration.
	mu.Lock()
	w := winner
	mu.Unlock()
	if w == -1 && ctx.Err() == nil {
		select {
		case <-fired:
		case <-ctx.Done():
		}
		// Re-check under the lock: a callback that won the race against
		// the cancellation must not be missed.
		mu.Lock()
		w = winner
		mu.Unlock()
	}

	unsubscribeAll()
	if w == -1 {
		return -1, ctx.Err()
	}
	return w, nil
}
