// Package queue provides a bounded, closeable FIFO queue for handoff
// between producers and a background processor.
//
// Semantics:
//   - Enqueue blocks while the queue is full, Dequeue blocks while it is
//     empty; both honor context cancellation.
//   - Close races with Enqueue are decided at an in-lock commit point:
//     an item committed before the close takes effect remains drainable;
//     an Enqueue that has not committed returns ErrClosed.
//   - Cancelling a wait never consumes an element or frees/leaks capacity;
//     a cancelled operation has no observable effect.
//   - After Close, readers drain all committed items; once drained, every
//     Dequeue returns ErrClosed (termination).
package queue

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Enqueue when the queue is already closed, and by
// Dequeue once the queue is closed and fully drained.
var ErrClosed = errors.New("queue: closed")

// Queue is a bounded FIFO queue safe for concurrent use.
type Queue[T any] struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state change
	waiters int           // number of goroutines blocked on changed
	buf     []T           // ring buffer, len(buf) == capacity
	head    int           // index of the oldest element
	size    int           // number of elements currently stored
	closed  bool
}

// New returns a queue with the given capacity. It panics if capacity < 1.
func New[T any](capacity int) *Queue[T] {
	if capacity < 1 {
		panic("queue: capacity must be positive")
	}
	return &Queue[T]{
		changed: make(chan struct{}),
		buf:     make([]T, capacity),
	}
}

// signalLocked wakes all waiters. Callers must hold q.mu.
func (q *Queue[T]) signalLocked() {
	if q.waiters > 0 {
		close(q.changed)
		q.changed = make(chan struct{})
	}
}

// waitLocked blocks until the queue state changes or ctx is done.
// Callers must hold q.mu; it is held again when waitLocked returns.
// The returned error is nil on a state change, ctx.Err() on cancellation.
func (q *Queue[T]) waitLocked(ctx context.Context) error {
	q.waiters++
	ch := q.changed
	q.mu.Unlock()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-ch:
	}
	q.mu.Lock()
	q.waiters--
	return err
}

// Enqueue appends v to the queue, blocking while the queue is full.
// It returns ErrClosed if the queue is closed before v is committed, and
// ctx.Err() if the wait is cancelled; a cancelled Enqueue never commits.
func (q *Queue[T]) Enqueue(ctx context.Context, v T) error {
	q.mu.Lock()
	for q.size == len(q.buf) && !q.closed {
		if err := q.waitLocked(ctx); err != nil {
			q.mu.Unlock()
			return err
		}
	}
	if q.closed {
		q.mu.Unlock()
		return ErrClosed
	}
	// Commit point: the item becomes visible to consumers atomically
	// with respect to Close while the lock is held.
	q.buf[(q.head+q.size)%len(q.buf)] = v
	q.size++
	q.signalLocked()
	q.mu.Unlock()
	return nil
}

// Dequeue removes and returns the oldest item, blocking while the queue is
// empty. Once the queue is closed and drained it returns ErrClosed.
// A cancelled Dequeue never removes an item.
func (q *Queue[T]) Dequeue(ctx context.Context) (T, error) {
	q.mu.Lock()
	for q.size == 0 && !q.closed {
		if err := q.waitLocked(ctx); err != nil {
			q.mu.Unlock()
			var zero T
			return zero, err
		}
	}
	if q.size == 0 { // closed and drained
		q.mu.Unlock()
		var zero T
		return zero, ErrClosed
	}
	v := q.buf[q.head]
	var zero T
	q.buf[q.head] = zero
	q.head = (q.head + 1) % len(q.buf)
	q.size--
	q.signalLocked()
	q.mu.Unlock()
	return v, nil
}

// Close marks the queue closed. Committed items remain drainable; blocked
// and future Enqueue calls fail with ErrClosed. Close is idempotent.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.signalLocked()
	}
	q.mu.Unlock()
}

// Len reports the number of items currently in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// Cap reports the queue capacity.
func (q *Queue[T]) Cap() int { return len(q.buf) }

// Closed reports whether Close has been called.
func (q *Queue[T]) Closed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}
