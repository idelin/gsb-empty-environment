// Package queue implements a high-performance lock-free MPMC FIFO queue
// based on the Michael-Scott algorithm. All synchronization uses atomic
// load/store and compare-and-swap; there is no mutex anywhere.
//
// Memory-reclamation note: Go's garbage collector safely reclaims unlinked
// nodes, so the ABA/hazard-pointer machinery required in C is unnecessary.
package queue

import "sync/atomic"

// node is an internal linked-list cell.
type node[T any] struct {
	val  T
	next atomic.Pointer[node[T]]
}

// Queue is a lock-free multi-producer multi-consumer FIFO queue.
// The zero value is not ready for use; create queues with New.
type Queue[T any] struct {
	head atomic.Pointer[node[T]] // dummy node; first real element is head.next
	tail atomic.Pointer[node[T]]
	len  atomic.Int64 // approximate length, exact at quiescent points
}

// New returns an empty queue anchored at a shared dummy node.
func New[T any]() *Queue[T] {
	q := &Queue[T]{}
	dummy := &node[T]{}
	q.head.Store(dummy)
	q.tail.Store(dummy)
	return q
}

// Enqueue appends v to the tail. It never blocks and never fails.
func (q *Queue[T]) Enqueue(v T) {
	n := &node[T]{val: v}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() { // tail moved concurrently; retry
			continue
		}
		if next != nil {
			// Tail is lagging: help advance it, then retry.
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		// CAS-link the new node; exactly one competitor wins.
		if tail.next.CompareAndSwap(nil, n) {
			// Best-effort tail swing; another thread may do it for us.
			q.tail.CompareAndSwap(tail, n)
			q.len.Add(1)
			return
		}
	}
}

// Dequeue removes and returns the head element. ok is false when the
// queue was observed empty.
func (q *Queue[T]) Dequeue() (v T, ok bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head != q.head.Load() { // head moved concurrently; retry
			continue
		}
		if head == tail {
			if next == nil {
				var zero T
				return zero, false // genuinely empty
			}
			// Tail is lagging behind a half-finished enqueue: help it.
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		// Read the value before CAS: after a successful swing, head may be
		// reclaimed by another dequeuer.
		v = next.val
		if q.head.CompareAndSwap(head, next) {
			q.len.Add(-1)
			return v, true
		}
	}
}

// Len returns the current element count. It is exact whenever no
// Enqueue/Dequeue is in flight, and a close estimate otherwise.
func (q *Queue[T]) Len() int {
	n := q.len.Load()
	if n < 0 { // transient during racing enq/deq
		return 0
	}
	return int(n)
}

// Empty reports whether the queue is currently empty.
func (q *Queue[T]) Empty() bool { return q.Len() == 0 }
