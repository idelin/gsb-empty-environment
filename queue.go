package cbitmap

import "sync/atomic"

// qnode is a node of the lock-free queue.
type qnode[T any] struct {
	val  T
	next atomic.Pointer[qnode[T]]
}

// Queue is an unbounded multi-producer multi-consumer lock-free FIFO
// queue (Michael & Scott algorithm). All operations are safe for
// concurrent use without any mutex.
type Queue[T any] struct {
	head atomic.Pointer[qnode[T]]
	tail atomic.Pointer[qnode[T]]
	len  atomic.Int64
}

// NewQueue creates an empty lock-free queue.
func NewQueue[T any]() *Queue[T] {
	dummy := &qnode[T]{}
	q := &Queue[T]{}
	q.head.Store(dummy)
	q.tail.Store(dummy)
	return q
}

// Enqueue appends v to the tail of the queue.
func (q *Queue[T]) Enqueue(v T) {
	n := &qnode[T]{val: v}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail == q.tail.Load() { // tail/next still consistent
			if next == nil {
				if tail.next.CompareAndSwap(nil, n) {
					// Enqueue done; swing tail (helping is fine if this fails).
					q.tail.CompareAndSwap(tail, n)
					q.len.Add(1)
					return
				}
			} else {
				// Tail was lagging; help advance it.
				q.tail.CompareAndSwap(tail, next)
			}
		}
	}
}

// Dequeue removes and returns the head value. ok is false when empty.
func (q *Queue[T]) Dequeue() (v T, ok bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head == q.head.Load() { // head/tail/next still consistent
			if head == tail {
				if next == nil {
					var zero T
					return zero, false // genuinely empty
				}
				// Tail was lagging; help advance it.
				q.tail.CompareAndSwap(tail, next)
			} else {
				// Read value before CAS; only the CAS winner may use it.
				v = next.val
				if q.head.CompareAndSwap(head, next) {
					q.len.Add(-1)
					return v, true
				}
			}
		}
	}
}

// Len returns the approximate number of elements in the queue.
func (q *Queue[T]) Len() int {
	n := q.len.Load()
	if n < 0 {
		return 0
	}
	return int(n)
}

// IsEmpty reports whether the queue is currently empty.
func (q *Queue[T]) IsEmpty() bool {
	head := q.head.Load()
	return head == q.tail.Load() && head.next.Load() == nil
}
