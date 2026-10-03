// Package corochan provides a Go-style coroutine Channel synchronization
// primitive and a lock-free MPMC ring queue.
package corochan

import (
	"errors"
	"runtime"
	"sync/atomic"
)

// ErrQueueFull is returned by RingQueue.Enqueue when the queue has no free slot.
var ErrQueueFull = errors.New("corochan: ring queue is full")

// ErrQueueEmpty is returned by RingQueue.Dequeue when the queue has no item.
var ErrQueueEmpty = errors.New("corochan: ring queue is empty")

// ringSlot is one cell of the ring buffer. The sequence number implements the
// Dmitry Vyukov bounded MPMC queue protocol: a slot is writable when
// seq == position, and readable when seq == position+1.
type ringSlot[T any] struct {
	seq atomic.Uint64
	val T
}

// RingQueue is a bounded, lock-free, multi-producer multi-consumer FIFO queue.
// All operations are wait-free for the fast path and use CAS retries under
// contention; no goroutine ever blocks holding a lock.
type RingQueue[T any] struct {
	cap  uint64 // always a power of two
	mask uint64 // cap - 1
	head atomic.Uint64
	tail atomic.Uint64
	buf  []ringSlot[T]
}

// NewRingQueue creates a RingQueue holding at least capacity items. The
// capacity is rounded up to the next power of two (minimum 2).
func NewRingQueue[T any](capacity int) *RingQueue[T] {
	n := uint64(2)
	for n < uint64(capacity) {
		n <<= 1
	}
	q := &RingQueue[T]{
		cap:  n,
		mask: n - 1,
		buf:  make([]ringSlot[T], n),
	}
	for i := range q.buf {
		q.buf[i].seq.Store(uint64(i))
	}
	return q
}

// Cap returns the (rounded-up) capacity of the queue.
func (q *RingQueue[T]) Cap() int { return int(q.cap) }

// Enqueue inserts v at the tail. It returns ErrQueueFull without blocking if
// the queue is full.
func (q *RingQueue[T]) Enqueue(v T) error {
	for {
		tail := q.tail.Load()
		slot := &q.buf[tail&q.mask]
		seq := slot.seq.Load()
		switch diff := int64(seq) - int64(tail); {
		case diff == 0:
			// Slot is free; claim this tail position.
			if q.tail.CompareAndSwap(tail, tail+1) {
				slot.val = v
				slot.seq.Store(tail + 1) // publish
				return nil
			}
		case diff < 0:
			return ErrQueueFull
		default:
			// Another producer claimed the slot between our loads; retry.
			runtime.Gosched()
		}
	}
}

// Dequeue removes and returns the oldest item. It returns ErrQueueEmpty
// without blocking if the queue is empty.
func (q *RingQueue[T]) Dequeue() (T, error) {
	for {
		head := q.head.Load()
		slot := &q.buf[head&q.mask]
		seq := slot.seq.Load()
		switch diff := int64(seq) - int64(head+1); {
		case diff == 0:
			// Slot holds the item for this head position; claim it.
			if q.head.CompareAndSwap(head, head+1) {
				v := slot.val
				var zero T
				slot.val = zero
				slot.seq.Store(head + q.cap) // mark free for the next lap
				return v, nil
			}
		case diff < 0:
			return zeroOf[T](), ErrQueueEmpty
		default:
			runtime.Gosched()
		}
	}
}

func zeroOf[T any]() T {
	var z T
	return z
}
