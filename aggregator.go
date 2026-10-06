// Package aggregator batches writes to a Sink and hands back an independent
// receipt for every submitted item.
//
// Batches are flushed when they reach a configured size or when the caller
// explicitly requests a barrier. A Sink may report a short write: the
// successful prefix of the batch is acknowledged and every remaining item
// fails its receipt with the batch error.
package aggregator

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is delivered on receipts and returned by Close when the
// aggregator has already been closed.
var ErrClosed = errors.New("aggregator: closed")

// ErrShortWrite fails the unwritten tail of a batch when the Sink reports a
// short write without an error of its own.
var ErrShortWrite = errors.New("aggregator: short write")

// Sink consumes one batch of items at a time.
//
// WriteBatch must return the number of items, counted from the front of the
// batch, that were written successfully. Items beyond that prefix are
// considered failed; err describes that failure. Returning n < len(items)
// with a nil err is a short write and fails the tail with ErrShortWrite.
type Sink interface {
	WriteBatch(ctx context.Context, items [][]byte) (n int, err error)
}

type item struct {
	data []byte
	done chan error
}

// Aggregator collects items and flushes them to a Sink in batches.
//
// The zero value is not usable; construct one with New. All methods are safe
// for concurrent use.
type Aggregator struct {
	sink     Sink
	maxBatch int

	// mu guards pending, closed and errSinceBarrier.
	mu              sync.Mutex
	pending         []item
	closed          bool
	errSinceBarrier error

	// flushMu serializes flushes so batches reach the Sink in order and
	// Close can wait for in-flight flushes by acquiring it.
	flushMu sync.Mutex
}

// New returns an Aggregator that flushes to sink once maxBatch items are
// pending. It panics if sink is nil or maxBatch < 1.
func New(sink Sink, maxBatch int) *Aggregator {
	if sink == nil {
		panic("aggregator: nil sink")
	}
	if maxBatch < 1 {
		panic("aggregator: maxBatch must be >= 1")
	}
	return &Aggregator{sink: sink, maxBatch: maxBatch}
}

// Submit enqueues data and returns a buffered channel that receives exactly
// one receipt: nil once the item is durably written, or the error that
// failed it. Submitting after Close yields ErrClosed immediately.
//
// The data slice is copied and may be reused by the caller. Submit blocks
// while flushing if its item completes a full batch.
func (a *Aggregator) Submit(data []byte) <-chan error {
	done := make(chan error, 1)

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		done <- ErrClosed
		return done
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	a.pending = append(a.pending, item{data: buf, done: done})
	full := len(a.pending) >= a.maxBatch
	a.mu.Unlock()

	if full {
		a.flushMu.Lock()
		a.flushLocked(context.Background())
		a.flushMu.Unlock()
	}
	return done
}

// Barrier flushes every item submitted so far and blocks until the flush
// completes. It reports any failure since the previous Barrier or Close —
// including failures of batches flushed automatically by size — so a failed
// batch is never silently forgotten before the next barrier. A nil return
// means every item submitted before the barrier was written successfully.
func (a *Aggregator) Barrier(ctx context.Context) error {
	a.flushMu.Lock()
	defer a.flushMu.Unlock()
	a.flushLocked(ctx)
	return a.drainErr()
}

// Close stops accepting new items, flushes everything still pending, waits
// for in-flight flushes, and resolves every outstanding receipt. It returns
// any failure accumulated since the last Barrier, including the final flush.
// Closing twice returns ErrClosed.
func (a *Aggregator) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.closed = true
	a.mu.Unlock()

	a.flushMu.Lock()
	defer a.flushMu.Unlock()
	a.flushLocked(ctx)
	return a.drainErr()
}

// flushLocked moves the pending items into one batch and writes it to the
// sink, resolving each receipt. The caller must hold flushMu.
func (a *Aggregator) flushLocked(ctx context.Context) {
	a.mu.Lock()
	batch := a.pending
	a.pending = nil
	a.mu.Unlock()

	if len(batch) == 0 {
		return
	}

	datas := make([][]byte, len(batch))
	for i, it := range batch {
		datas[i] = it.data
	}

	n, err := a.sink.WriteBatch(ctx, datas)
	if n < 0 {
		n = 0
	}
	if n > len(batch) {
		n = len(batch)
	}

	batchErr := err
	if batchErr == nil && n < len(batch) {
		batchErr = ErrShortWrite
	}

	for i, it := range batch {
		if i < n {
			it.done <- nil
		} else {
			it.done <- batchErr
		}
	}

	if batchErr != nil {
		a.mu.Lock()
		a.errSinceBarrier = errors.Join(a.errSinceBarrier, batchErr)
		a.mu.Unlock()
	}
}

// drainErr returns and clears the failure accumulated since the last drain.
// The caller must hold flushMu so no concurrent flush can record a failure
// between the read and the reset.
func (a *Aggregator) drainErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.errSinceBarrier
	a.errSinceBarrier = nil
	return err
}
