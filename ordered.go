// Package ordered runs tasks concurrently while delivering their results
// in strict input order.
//
// Map consumes elements from an input channel, executes a task function
// on at most window elements concurrently, and delivers results on the
// returned channel in the exact order the elements were read. At most
// window results may be completed but undelivered, so a slow head element
// applies backpressure to both task execution and input consumption, and
// infinite inputs are never prefetched beyond the window.
package ordered

import (
	"context"
	"sync"
)

// Policy selects how task errors are handled.
type Policy int

const (
	// FailFast aborts as soon as any task fails: the first failing result
	// is delivered as the final element (possibly ahead of earlier
	// indices), in-flight work is cancelled, and completed-but-undelivered
	// results are discarded.
	FailFast Policy = iota
	// OrderedFail delivers results in input order up to and including the
	// first failing task, then cancels the remaining work and discards
	// their results.
	OrderedFail
)

// Result is the outcome of the task for the input element at Index.
type Result[Out any] struct {
	Index int
	Value Out
	Err   error
}

// Map consumes elements from in, invokes fn on at most window elements
// concurrently, and delivers their results on the returned channel in
// strict input order.
//
// The window bounds both in-flight tasks and completed-but-undelivered
// results, so in is never prefetched beyond window elements and a slow
// head element throttles the whole pipeline. The returned channel is
// closed once the input is exhausted, an error policy stops the stream,
// or ctx is cancelled. fn should honour ctx cancellation; the caller must
// keep receiving from the returned channel until it is closed.
func Map[In, Out any](ctx context.Context, in <-chan In, window int, policy Policy, fn func(context.Context, In) (Out, error)) <-chan Result[Out] {
	if window < 1 {
		window = 1
	}
	out := make(chan Result[Out])
	go func() {
		defer close(out)

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var wg sync.WaitGroup
		defer wg.Wait()

		// slots holds one buffered channel per in-flight task, in input
		// order; slots[0] is always the head (oldest) element.
		slots := make([]chan Result[Out], 0, window)
		// failed carries the first task error for the FailFast policy.
		failed := make(chan Result[Out], 1)

		index := 0
		launch := func(item In) {
			slot := make(chan Result[Out], 1)
			slots = append(slots, slot)
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				value, err := fn(ctx, item)
				r := Result[Out]{Index: idx, Value: value, Err: err}
				slot <- r
				if err != nil {
					select {
					case failed <- r:
					default:
					}
				}
			}(index)
			index++
		}

		input := in
		abort := false
		var final *Result[Out]
		for !abort && (input != nil || len(slots) > 0) {
			// Refill the window; never prefetch more than window elements.
			for input != nil && len(slots) < window && !abort {
				select {
				case item, ok := <-input:
					if !ok {
						input = nil
					} else {
						launch(item)
					}
				case r := <-failed:
					if policy == FailFast {
						final = &r
						abort = true
						input = nil
					}
				case <-ctx.Done():
					abort = true
					input = nil
				}
			}
			if abort || len(slots) == 0 {
				continue
			}

			// Wait for the head element; later elements may finish
			// earlier but can never overtake it.
			var r Result[Out]
			if policy == FailFast {
				select {
				case r = <-slots[0]:
				case fr := <-failed:
					final = &fr
					abort = true
				case <-ctx.Done():
					abort = true
				}
			} else {
				select {
				case r = <-slots[0]:
				case <-ctx.Done():
					abort = true
				}
			}
			if abort {
				continue
			}
			slots = slots[1:]

			select {
			case out <- r:
			case <-ctx.Done():
				abort = true
				continue
			}
			if r.Err != nil {
				// Both policies stop after delivering an error that
				// reached the head of the stream.
				abort = true
			}
		}

		if final != nil {
			select {
			case out <- *final:
			case <-ctx.Done():
			}
		}
		cancel()
		// Reclaim completed-but-undelivered results so no goroutine or
		// buffered slot is left behind.
		for _, s := range slots {
			<-s
		}
	}()
	return out
}
