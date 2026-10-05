// Package mpqueue implements a bounded multi-producer multi-consumer queue
// that distinguishes graceful shutdown (drain) from failure shutdown (abort).
//
// Shutdown semantics:
//   - Close: graceful. No new items are accepted; consumers keep draining
//     items already enqueued and observe ErrClosed once the queue is empty.
//   - Abort: failure. Everything stops immediately; blocked and future
//     consumers observe ErrAborted and the remaining buffered items are
//     handed to the Abort caller in FIFO order.
//
// Close and Abort may race; the first one to take effect wins and fixes the
// shutdown semantics. The loser is a no-op and reports it lost.
//
// Cancellation never swallows elements: a Dequeue cancelled while waiting
// takes nothing, and an Enqueue cancelled while waiting for capacity keeps
// ownership of its item with the producer.
//
// Every committed operation is appended to an in-memory, totally ordered
// history (Queue.History) that can be replayed (Replay) and audited for
// capacity and ownership conservation (Verify, Queue.Check).
package mpqueue

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrClosed is returned by Dequeue once a gracefully closed queue has
	// been fully drained, and by Enqueue on a closed queue.
	ErrClosed = errors.New("mpqueue: queue closed and drained")
	// ErrAborted is returned by Enqueue and Dequeue on an aborted queue.
	ErrAborted = errors.New("mpqueue: queue aborted")
)

type state int8

const (
	stateOpen state = iota
	stateDraining
	stateAborted
)

func (s state) String() string {
	switch s {
	case stateOpen:
		return "open"
	case stateDraining:
		return "draining"
	case stateAborted:
		return "aborted"
	}
	return "unknown"
}

// Queue is a bounded FIFO queue safe for concurrent use by multiple
// producers and consumers.
type Queue[T any] struct {
	mu    sync.Mutex
	buf   []T // ring buffer, fixed capacity
	head  int
	size  int
	state state
	wake  chan struct{} // closed and replaced on every state change

	seq      uint64
	hist     []Event[T]
	enqueued uint64
	dequeued uint64
	released uint64 // items handed to the Abort caller
}

// New returns a queue with the given positive capacity.
func New[T any](capacity int) *Queue[T] {
	if capacity <= 0 {
		panic("mpqueue: capacity must be positive")
	}
	return &Queue[T]{
		buf:  make([]T, capacity),
		wake: make(chan struct{}),
	}
}

// Cap returns the queue capacity.
func (q *Queue[T]) Cap() int { return len(q.buf) }

// Len returns the number of currently buffered items.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

func (q *Queue[T]) broadcastLocked() {
	close(q.wake)
	q.wake = make(chan struct{})
}

func (q *Queue[T]) pushLocked(v T) {
	q.buf[(q.head+q.size)%len(q.buf)] = v
	q.size++
	q.enqueued++
}

func (q *Queue[T]) popLocked() T {
	v := q.buf[q.head]
	var zero T
	q.buf[q.head] = zero // drop the reference so ownership is unambiguous
	q.head = (q.head + 1) % len(q.buf)
	q.size--
	return v
}

func (q *Queue[T]) recordLocked(op OpKind, v T, count int) {
	q.seq++
	q.hist = append(q.hist, Event[T]{Seq: q.seq, Op: op, Value: v, Count: count})
}

// Enqueue blocks until there is capacity for v, the queue shuts down, or
// ctx is done. If ctx is done while waiting, v is not consumed and remains
// owned by the caller.
func (q *Queue[T]) Enqueue(ctx context.Context, v T) error {
	q.mu.Lock()
	for {
		switch q.state {
		case stateDraining:
			q.mu.Unlock()
			return ErrClosed
		case stateAborted:
			q.mu.Unlock()
			return ErrAborted
		}
		if q.size < len(q.buf) {
			q.pushLocked(v)
			q.recordLocked(OpEnqueue, v, 0)
			q.broadcastLocked()
			q.mu.Unlock()
			return nil
		}
		wake := q.wake
		q.mu.Unlock()
		select {
		case <-wake:
			q.mu.Lock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Dequeue blocks until an item is available, the queue shuts down, or ctx
// is done. If ctx is done while waiting, no item is taken. After a graceful
// Close it keeps returning buffered items and reports ErrClosed only once
// the queue is empty; after Abort it reports ErrAborted immediately.
func (q *Queue[T]) Dequeue(ctx context.Context) (T, error) {
	q.mu.Lock()
	for {
		if q.size > 0 && q.state != stateAborted {
			v := q.popLocked()
			q.dequeued++
			q.recordLocked(OpDequeue, v, 0)
			q.broadcastLocked()
			q.mu.Unlock()
			return v, nil
		}
		switch q.state {
		case stateAborted:
			var zero T
			q.mu.Unlock()
			return zero, ErrAborted
		case stateDraining: // and empty
			var zero T
			q.mu.Unlock()
			return zero, ErrClosed
		}
		wake := q.wake
		q.mu.Unlock()
		select {
		case <-wake:
			q.mu.Lock()
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
}

// Close performs a graceful shutdown: producers are rejected and consumers
// drain the remaining items. It reports whether it won the shutdown race,
// i.e. whether it was the first of Close/Abort to take effect.
func (q *Queue[T]) Close() (won bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state != stateOpen {
		return false
	}
	q.state = stateDraining
	q.recordLocked(OpClose, *new(T), 0)
	q.broadcastLocked()
	return true
}

// Abort performs a failure shutdown: all waiters are woken with ErrAborted
// and the remaining buffered items are removed and returned to the caller
// in FIFO order, transferring ownership. It reports whether it won the
// shutdown race; a losing Abort returns (nil, false) and changes nothing.
func (q *Queue[T]) Abort() (remaining []T, won bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state != stateOpen {
		return nil, false
	}
	remaining = make([]T, 0, q.size)
	for q.size > 0 {
		remaining = append(remaining, q.popLocked())
	}
	q.released += uint64(len(remaining))
	q.state = stateAborted
	q.recordLocked(OpAbort, *new(T), len(remaining))
	q.broadcastLocked()
	return remaining, true
}

// Stats is a point-in-time snapshot of the queue's conservation counters.
type Stats struct {
	State    string
	Enqueued uint64 // items accepted from producers
	Dequeued uint64 // items handed to consumers
	Released uint64 // items handed to the Abort caller
	Buffered int    // items still owned by the queue
}

// Stats returns the current conservation counters.
func (q *Queue[T]) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{
		State:    q.state.String(),
		Enqueued: q.enqueued,
		Dequeued: q.dequeued,
		Released: q.released,
		Buffered: q.size,
	}
}

// Check verifies the capacity and ownership conservation invariants:
// every accepted item is owned by exactly one party (consumer, abort
// caller, or the queue itself) and the buffer never exceeds capacity.
func (q *Queue[T]) Check() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.size < 0 || q.size > len(q.buf) {
		return fmt.Errorf("mpqueue: capacity violated: %d buffered with capacity %d", q.size, len(q.buf))
	}
	if want := q.dequeued + q.released + uint64(q.size); q.enqueued != want {
		return fmt.Errorf("mpqueue: ownership violated: enqueued=%d but dequeued=%d released=%d buffered=%d",
			q.enqueued, q.dequeued, q.released, q.size)
	}
	if q.state == stateAborted && q.size != 0 {
		return fmt.Errorf("mpqueue: aborted queue still owns %d items", q.size)
	}
	return nil
}

// History returns a copy of the committed operation log in linearization
// order. It can be audited with Verify and Replay.
func (q *Queue[T]) History() []Event[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]Event[T](nil), q.hist...)
}
