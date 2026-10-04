// Package waitfirst waits for the first of several one-shot events to
// complete, then cancels every other registration.
//
// The semantics are:
//
//   - Events may complete before or while they are being registered; such
//     completions are never missed.
//   - If several events have completed by the time the decision is made,
//     the lowest input index wins.
//   - The caller may cancel the wait through a context.
//   - Before WaitFirst returns, every registration it made is revoked, so
//     no subscription is left behind.
package waitfirst

import (
	"context"
	"sync/atomic"
)

// Event is a one-shot event source.
//
// Subscribe registers fn to be invoked at most once, when the event
// completes. If the event has already completed, Subscribe must invoke fn
// before returning. The returned unsubscribe function removes the
// registration; after it returns, fn will not be invoked again unless an
// invocation was already in flight. Unsubscribe must be idempotent.
type Event interface {
	Subscribe(fn func()) (unsubscribe func())
}

// WaitFirst blocks until one of events completes or ctx is cancelled.
//
// On success it returns the index of the winning event and nil. When
// several events have completed by decision time, the lowest index wins.
// On cancellation it returns -1 and ctx.Err(); a completion that races
// with cancellation still wins, so a completed event is never reported as
// a cancellation.
//
// With no events, WaitFirst blocks until ctx is cancelled.
//
// Every registration is revoked before WaitFirst returns.
func WaitFirst(ctx context.Context, events []Event) (int, error) {
	completed := make([]atomic.Bool, len(events))
	// wake has capacity 1: callbacks only need to ensure the waiter
	// re-scans, never to queue one token per completion.
	wake := make(chan struct{}, 1)

	unsubscribes := make([]func(), len(events))
	for i, ev := range events {
		i := i
		unsubscribes[i] = ev.Subscribe(func() {
			completed[i].Store(true)
			select {
			case wake <- struct{}{}:
			default:
			}
		})
	}
	// Revoke every registration on return. Unsubscribing the winner is a
	// no-op because its registration was already consumed by firing.
	defer func() {
		for _, unsubscribe := range unsubscribes {
			unsubscribe()
		}
	}()

	// scan returns the lowest index among the events observed completed.
	scan := func() int {
		for i := range completed {
			if completed[i].Load() {
				return i
			}
		}
		return -1
	}

	for {
		if idx := scan(); idx >= 0 {
			return idx, nil
		}
		select {
		case <-wake:
			// A completion was signalled; loop around and re-scan.
		case <-ctx.Done():
			// Final scan: a completion racing with cancellation still
			// wins, so a completed event is never missed.
			if idx := scan(); idx >= 0 {
				return idx, nil
			}
			return -1, ctx.Err()
		}
	}
}
