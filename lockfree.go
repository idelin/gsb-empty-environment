package aries

import "sync/atomic"

// LSNAllocator hands out monotonically increasing log sequence numbers
// using a CAS loop instead of a mutex, so concurrent transactions can
// reserve LSNs without ever blocking on a global lock.
type LSNAllocator struct {
	counter atomic.Uint64
}

// NewLSNAllocator returns an allocator whose first issued LSN is
// startFrom+1.
func NewLSNAllocator(startFrom uint64) *LSNAllocator {
	a := &LSNAllocator{}
	a.counter.Store(startFrom)
	return a
}

// Next atomically reserves and returns the next LSN.
func (a *LSNAllocator) Next() uint64 {
	for {
		cur := a.counter.Load()
		if a.counter.CompareAndSwap(cur, cur+1) {
			return cur + 1
		}
	}
}

// Current returns the most recently issued LSN.
func (a *LSNAllocator) Current() uint64 { return a.counter.Load() }

// queueNode is an internal linked-list node for LockFreeQueue.
type queueNode struct {
	val  any
	next atomic.Pointer[queueNode]
}

// LockFreeQueue is an unbounded MPMC FIFO queue implementing the
// Michael-Scott algorithm. All synchronization is done with atomic
// CAS operations on head/tail pointers; there are no mutexes, so
// producers and consumers never block each other. Go's GC makes the
// algorithm ABA-safe without hazard pointers.
type LockFreeQueue struct {
	head  atomic.Pointer[queueNode]
	tail  atomic.Pointer[queueNode]
	count atomic.Int64
}

// NewLockFreeQueue creates an empty queue anchored by a dummy node.
func NewLockFreeQueue() *LockFreeQueue {
	q := &LockFreeQueue{}
	dummy := &queueNode{}
	q.head.Store(dummy)
	q.tail.Store(dummy)
	return q
}

// Enqueue appends v to the tail of the queue.
func (q *LockFreeQueue) Enqueue(v any) {
	n := &queueNode{val: v}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() { // tail moved; retry
			continue
		}
		if next == nil {
			// Try to link the new node at the end of the list.
			if tail.next.CompareAndSwap(nil, n) {
				// Swing the tail; if another thread beats us to it,
				// that is fine — the queue is still consistent.
				q.tail.CompareAndSwap(tail, n)
				q.count.Add(1)
				return
			}
		} else {
			// Tail was lagging behind; help advance it.
			q.tail.CompareAndSwap(tail, next)
		}
	}
}

// Dequeue removes and returns the head value. The second return value
// is false when the queue is empty.
func (q *LockFreeQueue) Dequeue() (any, bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head != q.head.Load() { // head moved; retry
			continue
		}
		if head == tail {
			if next == nil {
				return nil, false // genuinely empty
			}
			// Tail is lagging behind a pending enqueue; help it.
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		// Read the value before CAS; on success it is ours.
		v := next.val
		if q.head.CompareAndSwap(head, next) {
			q.count.Add(-1)
			return v, true
		}
	}
}

// Len returns the current number of elements. It is exact as long as
// no concurrent Enqueue/Dequeue is in flight.
func (q *LockFreeQueue) Len() int64 { return q.count.Load() }

// IsEmpty reports whether the queue currently holds no elements.
func (q *LockFreeQueue) IsEmpty() bool { return q.count.Load() == 0 }
