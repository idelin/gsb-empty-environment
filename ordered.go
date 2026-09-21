// Package ordered runs tasks concurrently while delivering results strictly
// in input order, with a bounded completed-but-undelivered buffer.
package ordered

import "context"

// Policy decides how a task failure terminates the output stream.
type Policy int

const (
	// FailFast delivers the first error as soon as it happens, cancels the
	// remaining work, and discards every undelivered result.
	FailFast Policy = iota
	// FailInOrder keeps delivering results in input order; the first failed
	// item's error is delivered in sequence, then the stream terminates and
	// every later undelivered result is discarded.
	FailInOrder
)

// Options tunes concurrency, buffering and the failure policy.
type Options struct {
	// Workers bounds concurrently executing tasks (and therefore input prefetch).
	Workers int
	// MaxBuffered bounds results that completed but are not yet delivered.
	// When the head item is slow, at most MaxBuffered later results may pile
	// up before workers (and hence input consumption) are throttled.
	MaxBuffered int
	// Policy selects the failure strategy.
	Policy Policy
}

// Result is one ordered output element.
type Result[O any] struct {
	Value O
	Err   error
}

// Process consumes items from in, runs fn on them with at most opts.Workers
// concurrent executions, and returns a channel that yields results strictly
// in input order. The returned channel is closed when the input is exhausted
// and fully delivered, when a failure terminates the stream per opts.Policy,
// or when ctx is cancelled. Cancelling ctx reclaims all in-flight work.
func Process[I, O any](ctx context.Context, in <-chan I, fn func(context.Context, I) (O, error), opts Options) <-chan Result[O] {
	if opts.Workers < 1 {
		opts.Workers = 1
	}
	if opts.MaxBuffered < 1 {
		opts.MaxBuffered = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	out := make(chan Result[O])

	type slot struct {
		ready chan struct{} // closed once res is published
		grant chan struct{} // buffer token, handed out in input order
		res   Result[O]
	}

	slots := make(chan *slot)       // dispatcher -> deliverer, in input order
	firstErr := make(chan error, 1) // first failure wins (FailFast)

	// Dispatcher: pulls input only while a worker slot is free, so an
	// infinite input stream is never prefetched without bound.
	go func() {
		defer close(slots)
		inflight := make(chan struct{}, opts.Workers)
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-in:
				if !ok {
					return
				}
				select {
				case inflight <- struct{}{}:
				case <-ctx.Done():
					return
				}
				s := &slot{ready: make(chan struct{}), grant: make(chan struct{}, 1)}
				select {
				case slots <- s:
				case <-ctx.Done():
					<-inflight
					return
				}
				go func() {
					defer func() { <-inflight }()
					var res Result[O]
					v, err := fn(ctx, item)
					if err != nil {
						res.Err = err
						if opts.Policy == FailFast {
							select {
							case firstErr <- err:
							default:
							}
						}
					} else {
						res.Value = v
					}
					// Wait for a buffer token before publishing; tokens are
					// granted in input order, so the head can never starve.
					// On cancellation the result is recycled (dropped).
					select {
					case <-s.grant:
						s.res = res
						close(s.ready)
					case <-ctx.Done():
					}
				}()
			}
		}
	}()

	// Deliverer: emits slot results in input order.
	go func() {
		defer close(out)
		defer cancel()

		pending := make(map[int]*slot)
		received, nextEmit, nextGrant := 0, 0, 0
		slotsOpen := true

		// grant hands buffer tokens to slots inside the window
		// [nextEmit, nextEmit+MaxBuffered); because grants are issued in
		// input order, the head slot always obtains one.
		grant := func() {
			for nextGrant < nextEmit+opts.MaxBuffered {
				s, ok := pending[nextGrant]
				if !ok {
					return
				}
				s.grant <- struct{}{}
				nextGrant++
			}
		}

		emit := func(r Result[O]) bool {
			select {
			case out <- r:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for {
			if !slotsOpen && nextEmit == received {
				return
			}
			var readyCh <-chan struct{}
			if s, ok := pending[nextEmit]; ok {
				readyCh = s.ready
			}
			var errCh <-chan error
			if opts.Policy == FailFast {
				errCh = firstErr
			}
			select {
			case s, ok := <-slots:
				if !ok {
					slotsOpen = false
					slots = nil
					continue
				}
				pending[received] = s
				received++
				grant()
			case <-readyCh:
				res := pending[nextEmit].res
				delete(pending, nextEmit)
				nextEmit++
				if !emit(res) {
					return
				}
				if res.Err != nil && opts.Policy == FailInOrder {
					return
				}
				grant()
			case err := <-errCh:
				emit(Result[O]{Err: err})
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}
