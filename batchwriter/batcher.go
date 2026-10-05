// Package batchwriter buffers individual write requests and flushes them to
// an underlying store in batches. A batch is flushed when it reaches
// MaxSize items or when the oldest buffered request has waited MaxDelay,
// whichever comes first.
package batchwriter

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCancelled is delivered to a request that was cancelled before commit.
var ErrCancelled = errors.New("batchwriter: request cancelled")

// ErrClosed is delivered to requests submitted after Close.
var ErrClosed = errors.New("batchwriter: closed")

// WriteFunc persists one batch. The returned error is delivered to every
// member of that batch; other batches are unaffected.
type WriteFunc[T any] func(ctx context.Context, items []T) error

// Config configures a Batcher.
type Config[T any] struct {
	// MaxSize is the number of buffered requests that triggers an
	// immediate flush. Must be >= 1.
	MaxSize int
	// MaxDelay is the longest the oldest buffered request may wait before
	// its batch is flushed. The delay is anchored to the oldest request:
	// new arrivals never extend it, so they cannot starve old ones.
	// Must be > 0.
	MaxDelay time.Duration
	// Write persists a flushed batch. Required.
	Write WriteFunc[T]
	// Clock is optional; nil means RealClock.
	Clock Clock
}

// Batcher buffers requests and flushes them in batches. It is safe for
// concurrent use.
type Batcher[T any] struct {
	maxSize  int
	maxDelay time.Duration
	write    WriteFunc[T]
	clock    Clock

	mu      sync.Mutex
	pending []*Request[T]
	index   map[uint64]*Request[T]
	timer   Timer
	seq     uint64
	closed  bool

	// writeMu serializes calls to Write so batches reach the store in the
	// order they were cut.
	writeMu sync.Mutex
}

// New creates a Batcher. It panics on invalid configuration.
func New[T any](cfg Config[T]) *Batcher[T] {
	if cfg.MaxSize < 1 {
		panic("batchwriter: MaxSize must be >= 1")
	}
	if cfg.MaxDelay <= 0 {
		panic("batchwriter: MaxDelay must be > 0")
	}
	if cfg.Write == nil {
		panic("batchwriter: Write is required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock{}
	}
	return &Batcher[T]{
		maxSize:  cfg.MaxSize,
		maxDelay: cfg.MaxDelay,
		write:    cfg.Write,
		clock:    clock,
		index:    make(map[uint64]*Request[T]),
	}
}

// Request is a handle to one submitted item.
type Request[T any] struct {
	id   uint64
	item T
	done chan error // buffered, receives exactly one terminal result
	b    *Batcher[T]
}

// Submit buffers item and returns a handle for its eventual result.
// If the buffer reaches MaxSize the batch is flushed before Submit returns.
func (b *Batcher[T]) Submit(item T) *Request[T] {
	b.mu.Lock()
	b.seq++
	req := &Request[T]{id: b.seq, item: item, done: make(chan error, 1), b: b}
	if b.closed {
		b.mu.Unlock()
		req.done <- ErrClosed
		return req
	}
	b.pending = append(b.pending, req)
	b.index[req.id] = req
	if len(b.pending) == 1 {
		// Arm the deadline for the oldest request. Later arrivals only
		// join this batch; they never extend the deadline.
		b.timer = b.clock.AfterFunc(b.maxDelay, b.flush)
	}
	full := len(b.pending) >= b.maxSize
	b.mu.Unlock()

	if full {
		b.flush()
	}
	return req
}

// Cancel removes the request if it has not been committed to a batch yet.
// It reports whether the request was still pending. A request whose batch
// has already been cut cannot be cancelled and will be written.
func (r *Request[T]) Cancel() bool {
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.index[r.id]; !ok {
		return false
	}
	delete(b.index, r.id)
	for i, p := range b.pending {
		if p.id == r.id {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			break
		}
	}
	if len(b.pending) == 0 && b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	r.done <- ErrCancelled
	return true
}

// Done returns a channel that receives exactly one result: nil on success,
// the batch's write error, ErrCancelled, or ErrClosed.
func (r *Request[T]) Done() <-chan error { return r.done }

// Wait blocks until the request resolves and returns its result.
func (r *Request[T]) Wait() error { return <-r.done }

// flush cuts the current pending batch (if any) and writes it. The cut is
// atomic with respect to Submit and Cancel: a request is either in the cut
// batch or in the next one, never both and never lost.
func (b *Batcher[T]) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.pending
	b.pending = nil
	b.index = make(map[uint64]*Request[T])
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	items := make([]T, len(batch))
	for i, r := range batch {
		items[i] = r.item
	}

	b.writeMu.Lock()
	err := b.write(context.Background(), items)
	for _, r := range batch {
		r.done <- err
	}
	b.writeMu.Unlock()
}

// Close rejects future submissions and flushes whatever is still buffered.
func (b *Batcher[T]) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	b.flush()
}
