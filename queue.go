// Package boundedq provides a bounded, closable FIFO queue for background
// processors, plus a linearizability checker for histories of queue
// operations.
//
// Close semantics are arbitrated by a commit point inside the queue lock:
// an Enqueue that commits (appends) before Close takes the lock stays in
// the queue and remains drainable; an Enqueue that finds the queue already
// closed fails with ErrClosed. Cancelled waits never reserve elements or
// capacity, so a cancelled Enqueue/Dequeue cannot swallow either. Once the
// queue is closed and fully drained, every Dequeue returns ErrClosed.
package boundedq

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Enqueue when the queue is already closed, and by
// Dequeue when the queue is closed and fully drained.
var ErrClosed = errors.New("boundedq: queue closed")

// Queue is a bounded FIFO queue safe for concurrent use. The zero value is
// not usable; construct one with New.
type Queue[T any] struct {
	mu     sync.Mutex
	buf    []T // ring buffer, len(buf) == capacity
	head   int // index of the oldest element
	size   int // number of elements currently stored
	closed bool

	// Generation channels used to broadcast state changes to blocked
	// waiters. Waiters capture the channel under the lock, then select on
	// it after unlocking; state changes close the channel and reset it to
	// nil so the next waiter gets a fresh generation. Waiters hold no
	// reservation while blocked, so context cancellation cannot consume
	// elements or capacity.
	enqWait chan struct{} // signalled when capacity frees up or queue closes
	deqWait chan struct{} // signalled when an element arrives or queue closes
}

// New returns a Queue with the given capacity. It panics if capacity <= 0.
func New[T any](capacity int) *Queue[T] {
	if capacity <= 0 {
		panic("boundedq: capacity must be positive")
	}
	return &Queue[T]{buf: make([]T, capacity)}
}

// Cap returns the queue capacity.
func (q *Queue[T]) Cap() int { return len(q.buf) }

// Len returns the number of elements currently in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// Closed reports whether Close has been called.
func (q *Queue[T]) Closed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

// Enqueue appends v to the queue, blocking while the queue is full. It
// returns ErrClosed if the queue is closed before the enqueue commits, and
// ctx.Err() if the context is cancelled while waiting. A cancelled wait
// never consumes capacity.
func (q *Queue[T]) Enqueue(ctx context.Context, v T) error {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return ErrClosed
		}
		if q.size < len(q.buf) {
			// Commit point: the append is serialized against Close by
			// the lock, so a committed element is always drainable.
			q.buf[(q.head+q.size)%len(q.buf)] = v
			q.size++
			q.broadcastLocked(&q.deqWait)
			q.mu.Unlock()
			return nil
		}
		wait := q.waiterLocked(&q.enqWait)
		q.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Dequeue removes and returns the oldest element, blocking while the queue
// is empty. It returns ErrClosed once the queue is closed and drained, and
// ctx.Err() if the context is cancelled while waiting. A cancelled wait
// never consumes an element.
func (q *Queue[T]) Dequeue(ctx context.Context) (T, error) {
	for {
		q.mu.Lock()
		if q.size > 0 {
			v := q.buf[q.head]
			var zero T
			q.buf[q.head] = zero
			q.head = (q.head + 1) % len(q.buf)
			q.size--
			q.broadcastLocked(&q.enqWait)
			q.mu.Unlock()
			return v, nil
		}
		if q.closed {
			var zero T
			q.mu.Unlock()
			return zero, ErrClosed
		}
		wait := q.waiterLocked(&q.deqWait)
		q.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
}

// Close marks the queue closed and wakes all waiters. Committed elements
// remain drainable; subsequent Enqueue calls fail with ErrClosed, and
// Dequeue returns ErrClosed once the queue is empty. Close is idempotent.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.broadcastLocked(&q.enqWait)
	q.broadcastLocked(&q.deqWait)
}

// waiterLocked returns the current generation channel for *ch, allocating
// one if needed. Callers must hold q.mu.
func (q *Queue[T]) waiterLocked(ch *chan struct{}) chan struct{} {
	if *ch == nil {
		*ch = make(chan struct{})
	}
	return *ch
}

// broadcastLocked wakes every waiter on *ch and starts a new generation.
// Callers must hold q.mu.
func (q *Queue[T]) broadcastLocked(ch *chan struct{}) {
	if *ch != nil {
		close(*ch)
		*ch = nil
	}
}
