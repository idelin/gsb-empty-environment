// Package queue provides a high-performance lock-free bounded MPMC queue.
//
// The implementation is Dmitry Vyukov's bounded MPMC queue: a ring buffer
// of sequence-numbered slots where producers and consumers coordinate
// exclusively through atomic compare-and-swap operations. There are no
// mutexes anywhere on the data path.
package queue

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// ErrFull is returned by Enqueue when the queue has no free slot.
var ErrFull = errors.New("queue: full")

// ErrEmpty is returned by Dequeue when the queue has no item.
var ErrEmpty = errors.New("queue: empty")

// cacheLinePad pads a value to a full cache line so that the hot head/tail
// counters never share a cache line (false sharing would serialize the CAS
// traffic of producers against consumers).
type cacheLinePad struct {
	_ [64]byte
}

type slot[T any] struct {
	seq atomic.Uint64
	val T
}

// MPMC is a bounded, lock-free, multi-producer multi-consumer FIFO queue.
// The zero value is not usable; construct with New.
type MPMC[T any] struct {
	_     cacheLinePad
	head  atomic.Uint64 // next dequeue position
	_     cacheLinePad
	tail  atomic.Uint64 // next enqueue position
	_     cacheLinePad
	mask  uint64
	cells []slot[T]
}

// New returns a queue with the given capacity, rounded up to a power of two.
// capacity must be >= 2.
func New[T any](capacity uint64) *MPMC[T] {
	if capacity < 2 {
		panic(fmt.Sprintf("queue: capacity %d too small, need >= 2", capacity))
	}
	size := uint64(2)
	for size < capacity {
		size <<= 1
	}
	q := &MPMC[T]{
		mask:  size - 1,
		cells: make([]slot[T], size),
	}
	for i := range q.cells {
		q.cells[i].seq.Store(uint64(i))
	}
	return q
}

// Cap returns the usable capacity of the queue.
func (q *MPMC[T]) Cap() uint64 { return q.mask + 1 }

// Len returns an approximate number of items currently in the queue.
// It is a snapshot under concurrency and may be stale by the time the
// caller acts on it.
func (q *MPMC[T]) Len() int {
	tail := q.tail.Load()
	head := q.head.Load()
	if tail <= head {
		return 0
	}
	n := tail - head
	if cap := q.Cap(); n > cap {
		return int(cap)
	}
	return int(n)
}

// Enqueue inserts v at the tail. It returns ErrFull if the queue is full.
// The operation is lock-free: a racing slow consumer can delay but never
// block other producers beyond a bounded number of retries.
func (q *MPMC[T]) Enqueue(v T) error {
	for {
		tail := q.tail.Load()
		cell := &q.cells[tail&q.mask]
		seq := cell.seq.Load()
		switch dif := int64(seq) - int64(tail); {
		case dif == 0:
			// Slot is free; try to claim this enqueue position.
			if q.tail.CompareAndSwap(tail, tail+1) {
				cell.val = v
				cell.seq.Store(tail + 1) // publish: slot now readable
				return nil
			}
		case dif < 0:
			return ErrFull
		default:
			// Another producer claimed the slot first; retry.
		}
	}
}

// Dequeue removes and returns the head item. It returns ErrEmpty if the
// queue is empty. The operation is lock-free.
func (q *MPMC[T]) Dequeue() (T, error) {
	for {
		head := q.head.Load()
		cell := &q.cells[head&q.mask]
		seq := cell.seq.Load()
		switch dif := int64(seq) - int64(head+1); {
		case dif == 0:
			// Slot holds the head item; try to claim this dequeue position.
			if q.head.CompareAndSwap(head, head+1) {
				v := cell.val
				var zero T
				cell.val = zero                   // release reference for GC
				cell.seq.Store(head + q.mask + 1) // publish: slot now writable
				return v, nil
			}
		case dif < 0:
			var zero T
			return zero, ErrEmpty
		default:
			// Another consumer claimed the slot first; retry.
		}
	}
}

// MustEnqueue enqueues v, panicking if the queue is full. Useful when the
// caller has reserved capacity.
func (q *MPMC[T]) MustEnqueue(v T) {
	if err := q.Enqueue(v); err != nil {
		panic(err)
	}
}
