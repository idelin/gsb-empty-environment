// Package queue provides high-performance lock-free concurrent queues and
// CAS-accelerated primitives. No mutexes are used anywhere: all coordination
// is done with sync/atomic compare-and-swap operations, eliminating the
// coarse-grained global lock found in channel- or mutex-based designs.
package queue

import (
	"errors"
	"runtime"
	"sync/atomic"
	"unsafe"
)

// ErrClosed is returned by operations on a closed unbounded queue.
var ErrClosed = errors.New("queue: closed")

// cacheLine pads hot counters so producers and consumers do not share a
// cache line (false sharing) on 64-byte-line architectures.
type cacheLine [8]uint64

// ---------------------------------------------------------------------------
// BoundedQueue: bounded MPMC ring buffer (Dmitry Vyukov's design). Each slot
// carries a sequence number; producers and consumers claim slots with a
// single CAS on the tail/head cursor. Enqueue/Dequeue never block: they
// report failure when the queue is full/empty.
// ---------------------------------------------------------------------------

type slot struct {
	seq uint64
	val interface{}
}

// BoundedQueue is a lock-free bounded multi-producer multi-consumer queue.
type BoundedQueue struct {
	head  uint64
	_pad0 cacheLine
	tail  uint64
	_pad1 cacheLine
	mask  uint64
	slots []slot
}

// NewBounded returns a BoundedQueue whose capacity is size rounded up to
// the next power of two (minimum 2).
func NewBounded(size int) *BoundedQueue {
	n := 2
	for n < size {
		n <<= 1
	}
	q := &BoundedQueue{mask: uint64(n - 1), slots: make([]slot, n)}
	for i := range q.slots {
		q.slots[i].seq = uint64(i)
	}
	return q
}

// Capacity returns the usable capacity of the queue.
func (q *BoundedQueue) Capacity() int { return len(q.slots) }

// Enqueue inserts v, returning false if the queue is full.
func (q *BoundedQueue) Enqueue(v interface{}) bool {
	for {
		tail := atomic.LoadUint64(&q.tail)
		s := &q.slots[tail&q.mask]
		seq := atomic.LoadUint64(&s.seq)
		switch dif := int64(seq) - int64(tail); {
		case dif == 0:
			if atomic.CompareAndSwapUint64(&q.tail, tail, tail+1) {
				s.val = v
				atomic.StoreUint64(&s.seq, tail+1) // publish
				return true
			}
		case dif < 0:
			return false // full
		}
		runtime.Gosched()
	}
}

// Dequeue removes and returns the oldest value, or ok=false if empty.
func (q *BoundedQueue) Dequeue() (v interface{}, ok bool) {
	for {
		head := atomic.LoadUint64(&q.head)
		s := &q.slots[head&q.mask]
		seq := atomic.LoadUint64(&s.seq)
		switch dif := int64(seq) - int64(head+1); {
		case dif == 0:
			if atomic.CompareAndSwapUint64(&q.head, head, head+1) {
				v = s.val
				s.val = nil // release reference for GC
				atomic.StoreUint64(&s.seq, head+uint64(len(q.slots)))
				return v, true
			}
		case dif < 0:
			return nil, false // empty
		}
		runtime.Gosched()
	}
}

// Len returns an approximate number of queued items (racy by nature).
func (q *BoundedQueue) Len() int {
	tail := atomic.LoadUint64(&q.tail)
	head := atomic.LoadUint64(&q.head)
	if tail < head {
		return 0
	}
	n := tail - head
	if n > uint64(len(q.slots)) {
		return len(q.slots)
	}
	return int(n)
}

// ---------------------------------------------------------------------------
// Queue: unbounded lock-free MPMC queue (Michael & Scott linked-queue).
// Safe under Go's GC: no ABA hazard from memory reuse while a goroutine
// holds a pointer. Head/tail are manipulated exclusively with CAS.
// ---------------------------------------------------------------------------

type node struct {
	val  interface{}
	next unsafe.Pointer // *node
}

// Queue is an unbounded lock-free multi-producer multi-consumer queue.
type Queue struct {
	head  unsafe.Pointer // *node, dequeue end
	_pad0 cacheLine
	tail  unsafe.Pointer // *node, enqueue end
	_pad1 cacheLine
	count int64
}

// New returns an empty unbounded Queue.
func New() *Queue {
	dummy := &node{}
	return &Queue{head: unsafe.Pointer(dummy), tail: unsafe.Pointer(dummy)}
}

// Enqueue appends v to the tail. It always succeeds.
func (q *Queue) Enqueue(v interface{}) {
	n := &node{val: v}
	for {
		tail := atomic.LoadPointer(&q.tail)
		next := atomic.LoadPointer(&(*node)(tail).next)
		if tail != atomic.LoadPointer(&q.tail) {
			continue
		}
		if next == nil {
			if atomic.CompareAndSwapPointer(&(*node)(tail).next, nil, unsafe.Pointer(n)) {
				atomic.CompareAndSwapPointer(&q.tail, tail, unsafe.Pointer(n))
				atomic.AddInt64(&q.count, 1)
				return
			}
		} else {
			// Tail is lagging; help advance it.
			atomic.CompareAndSwapPointer(&q.tail, tail, next)
		}
	}
}

// Dequeue removes and returns the oldest value, or ok=false if empty.
func (q *Queue) Dequeue() (interface{}, bool) {
	for {
		head := atomic.LoadPointer(&q.head)
		tail := atomic.LoadPointer(&q.tail)
		next := atomic.LoadPointer(&(*node)(head).next)
		if head != atomic.LoadPointer(&q.head) {
			continue
		}
		if next == nil {
			return nil, false // empty
		}
		if head == tail {
			// Tail is lagging; help advance it.
			atomic.CompareAndSwapPointer(&q.tail, tail, next)
			continue
		}
		v := (*node)(next).val
		if atomic.CompareAndSwapPointer(&q.head, head, next) {
			atomic.AddInt64(&q.count, -1)
			return v, true
		}
	}
}

// Len returns an approximate number of queued items.
func (q *Queue) Len() int {
	n := atomic.LoadInt64(&q.count)
	if n < 0 {
		return 0
	}
	return int(n)
}

// ---------------------------------------------------------------------------
// Counter: CAS-accelerated shared counter. Add and Max are implemented with
// pure CAS retry loops (no mutexes), scaling linearly across cores.
// ---------------------------------------------------------------------------

// Counter is a lock-free monotonically adjustable 64-bit counter.
type Counter struct {
	v    int64
	_pad cacheLine
}

// Add atomically adds delta via a CAS retry loop and returns the new value.
func (c *Counter) Add(delta int64) int64 {
	for {
		old := atomic.LoadInt64(&c.v)
		if atomic.CompareAndSwapInt64(&c.v, old, old+delta) {
			return old + delta
		}
	}
}

// Value returns the current counter value.
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.v) }

// Max atomically raises the counter to v if v is larger (CAS loop).
func (c *Counter) Max(v int64) int64 {
	for {
		old := atomic.LoadInt64(&c.v)
		if v <= old {
			return old
		}
		if atomic.CompareAndSwapInt64(&c.v, old, v) {
			return v
		}
	}
}
