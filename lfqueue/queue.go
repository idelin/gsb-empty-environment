// Package lfqueue implements a bounded multi-producer multi-consumer
// lock-free queue based on Dmitry Vyukov's sequence-numbered ring buffer.
//
// All synchronization is done with atomic load/CAS on per-cell sequence
// numbers plus the head/tail cursors. There is no mutex anywhere, so
// producers and consumers never block each other on a global lock.
package lfqueue

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// ErrFull is returned by Enqueue when the queue has no free slot.
var ErrFull = errors.New("lfqueue: queue is full")

// ErrEmpty is returned by Dequeue when the queue has no item.
var ErrEmpty = errors.New("lfqueue: queue is empty")

// cacheLinePad pads a cursor to its own cache line to avoid false sharing
// between the producer cursor, the consumer cursor and the cells array.
type cacheLinePad struct {
	_ [64]byte
}

type cell[T any] struct {
	seq atomic.Uint64
	val T
	_   [40]byte // keep cells on separate cache lines
}

// Queue is a bounded lock-free MPMC queue. The zero value is not usable;
// construct it with New.
type Queue[T any] struct {
	_     cacheLinePad
	head  atomic.Uint64 // next dequeue position
	_     cacheLinePad
	tail  atomic.Uint64 // next enqueue position
	_     cacheLinePad
	mask  uint64
	cells []cell[T]
}

// New creates a queue with the given capacity. The capacity is rounded up
// to the next power of two and must be at least 2.
func New[T any](capacity int) (*Queue[T], error) {
	if capacity < 2 {
		return nil, fmt.Errorf("lfqueue: capacity %d too small, need >= 2", capacity)
	}
	n := uint64(1)
	for n < uint64(capacity) {
		n <<= 1
	}
	q := &Queue[T]{
		mask:  n - 1,
		cells: make([]cell[T], n),
	}
	for i := range q.cells {
		q.cells[i].seq.Store(uint64(i))
	}
	return q, nil
}

// MustNew is like New but panics on invalid capacity.
func MustNew[T any](capacity int) *Queue[T] {
	q, err := New[T](capacity)
	if err != nil {
		panic(err)
	}
	return q
}

// Capacity returns the usable capacity of the queue.
func (q *Queue[T]) Capacity() int { return len(q.cells) }

// Enqueue inserts v. It returns ErrFull immediately (without blocking)
// when every slot is occupied.
func (q *Queue[T]) Enqueue(v T) error {
	var c *cell[T]
	tail := q.tail.Load()
	for {
		c = &q.cells[tail&q.mask]
		seq := c.seq.Load()
		dif := int64(seq) - int64(tail)
		switch {
		case dif == 0:
			// Slot is free for this lap; try to claim the tail cursor.
			if q.tail.CompareAndSwap(tail, tail+1) {
				c.val = v
				// Publish: mark the cell ready for consumers.
				c.seq.Store(tail + 1)
				return nil
			}
			// Lost the CAS race: another producer moved the tail.
			tail = q.tail.Load()
		case dif < 0:
			// The cell still holds an unconsumed value: queue full.
			return ErrFull
		default:
			// Another producer already claimed this tail; reload.
			tail = q.tail.Load()
		}
	}
}

// Dequeue removes and returns the oldest item. It returns ErrEmpty
// immediately (without blocking) when the queue is empty.
func (q *Queue[T]) Dequeue() (T, error) {
	var zero T
	head := q.head.Load()
	for {
		c := &q.cells[head&q.mask]
		seq := c.seq.Load()
		dif := int64(seq) - int64(head+1)
		switch {
		case dif == 0:
			if q.head.CompareAndSwap(head, head+1) {
				v := c.val
				c.val = zero // release reference: no memory leak
				// Free the cell for the next lap of producers.
				c.seq.Store(head + q.mask + 1)
				return v, nil
			}
			head = q.head.Load()
		case dif < 0:
			return zero, ErrEmpty
		default:
			head = q.head.Load()
		}
	}
}

// Len returns an approximate number of queued items. It is only exact
// when no concurrent Enqueue/Dequeue is in flight.
func (q *Queue[T]) Len() int {
	tail := q.tail.Load()
	head := q.head.Load()
	if tail < head {
		return 0
	}
	n := tail - head
	if n > uint64(len(q.cells)) {
		return len(q.cells)
	}
	return int(n)
}

// Empty reports whether the queue appears empty.
func (q *Queue[T]) Empty() bool { return q.Len() == 0 }
