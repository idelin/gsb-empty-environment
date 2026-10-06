// Package batchagg aggregates individual items into batches and flushes
// them to a Sink. A batch is flushed when it reaches a configured item
// count or when an explicit barrier (Flush) is requested. Every submitted
// item yields an independent Receipt that resolves exactly once with that
// item's own outcome.
package batchagg

import (
	"errors"
	"sync"
)

var (
	// ErrClosed is returned by Submit, Flush, and Close once the
	// aggregator has been closed.
	ErrClosed = errors.New("batchagg: aggregator is closed")
	// ErrShortWrite completes the receipts of items a Sink dropped by
	// reporting a short write without an error of its own.
	ErrShortWrite = errors.New("batchagg: short write")
)

// Sink consumes one flushed batch. It returns the number of items written
// from the prefix of batch. The first n items are reported as successful
// on their receipts; the remaining items fail with err, or with
// ErrShortWrite when err is nil.
type Sink[T any] interface {
	WriteBatch(batch []T) (n int, err error)
}

// Receipt is the independent outcome handle for one submitted item.
type Receipt struct {
	done chan struct{}
	err  error
}

func newReceipt() *Receipt {
	return &Receipt{done: make(chan struct{})}
}

func (r *Receipt) complete(err error) {
	r.err = err
	close(r.done)
}

// Done closes when the item's batch has been flushed and this item's
// outcome is known.
func (r *Receipt) Done() <-chan struct{} {
	return r.done
}

// Err blocks until Done closes and returns this item's outcome: nil on
// success, or the failure reported by the Sink.
func (r *Receipt) Err() error {
	<-r.done
	return r.err
}

// Options configures an Aggregator.
type Options struct {
	// MaxBatch is the number of buffered items that triggers an automatic
	// flush. Values <= 0 disable count-based flushing; batches are then
	// flushed only by Flush or Close.
	MaxBatch int
}

// Aggregator buffers items and flushes them to a Sink in batches.
//
// Flushes are serialized: while a Sink.WriteBatch call is in flight,
// Submit, Flush, and Close block until it returns. This keeps batch
// boundaries exact and makes Close wait for in-flight flushes before
// completing the remaining receipts.
type Aggregator[T any] struct {
	mu       sync.Mutex
	sink     Sink[T]
	maxBatch int

	buf  []T        // items not yet flushed
	recs []*Receipt // receipts parallel to buf

	pending []*Receipt // receipts not yet accounted for by a barrier
	closed  bool
}

// New returns an Aggregator writing to sink.
func New[T any](sink Sink[T], opts Options) *Aggregator[T] {
	return &Aggregator[T]{sink: sink, maxBatch: opts.MaxBatch}
}

// Submit buffers item and returns its independent Receipt. When the buffer
// reaches MaxBatch items, the batch is flushed before Submit returns.
// Submit returns ErrClosed after Close.
func (a *Aggregator[T]) Submit(item T) (*Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	r := newReceipt()
	a.buf = append(a.buf, item)
	a.recs = append(a.recs, r)
	a.pending = append(a.pending, r)
	if a.maxBatch > 0 && len(a.buf) >= a.maxBatch {
		a.flushLocked()
	}
	return r, nil
}

// Flush is a barrier: it flushes any buffered items and reports the
// combined outcome of every item submitted since the previous barrier,
// including items already flushed by the count trigger. A failed batch is
// therefore never hidden from a later barrier. Flush returns ErrClosed
// after Close.
func (a *Aggregator[T]) Flush() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.flushLocked()
	recs := a.pending
	a.pending = nil
	a.mu.Unlock()
	return joinReceipts(recs)
}

// Close stops the aggregator from accepting new items, waits for any
// in-flight flush, flushes the remaining buffer, and completes every
// outstanding receipt. It returns the combined outcome of all items not
// yet covered by a barrier. Closing twice returns ErrClosed.
func (a *Aggregator[T]) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.closed = true
	a.flushLocked()
	recs := a.pending
	a.pending = nil
	a.mu.Unlock()
	return joinReceipts(recs)
}

// flushLocked writes the current buffer to the sink and completes each
// receipt: the first n items succeed, the rest fail. The caller must hold
// a.mu.
func (a *Aggregator[T]) flushLocked() {
	if len(a.buf) == 0 {
		return
	}
	batch := a.buf
	recs := a.recs
	a.buf = nil
	a.recs = nil

	n, err := a.sink.WriteBatch(batch)
	if n < 0 {
		n = 0
	}
	if n > len(batch) {
		n = len(batch)
	}
	failErr := err
	if failErr == nil {
		failErr = ErrShortWrite
	}
	for i, r := range recs {
		if i < n {
			r.complete(nil)
		} else {
			r.complete(failErr)
		}
	}
}

func joinReceipts(recs []*Receipt) error {
	var errs []error
	for _, r := range recs {
		if err := r.Err(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
