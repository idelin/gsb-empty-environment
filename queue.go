package roaring

import "sync/atomic"

// LFQueue is a bounded, multi-producer multi-consumer lock-free FIFO queue
// based on Dmitry Vyukov's bounded MPMC queue. Every slot carries a
// sequence number; producers and consumers coordinate exclusively with
// atomic loads/stores and a single CAS per operation, so there is no
// mutex anywhere on the hot path.
type LFQueue struct {
	buf  []qslot
	mask int64
	_    [8]int64 // padding to keep head/tail on separate cache lines
	head int64
	_    [8]int64
	tail int64
	_    [8]int64
}

type qslot struct {
	seq int64
	val interface{}
}

// NewLFQueue creates a queue holding up to capacity elements. The capacity
// is rounded up to the next power of two (minimum 2).
func NewLFQueue(capacity int) *LFQueue {
	cap2 := 2
	for cap2 < capacity {
		cap2 <<= 1
	}
	q := &LFQueue{buf: make([]qslot, cap2), mask: int64(cap2 - 1)}
	for i := range q.buf {
		q.buf[i].seq = int64(i)
	}
	return q
}

// Capacity returns the usable capacity of the queue.
func (q *LFQueue) Capacity() int { return len(q.buf) }

// Push enqueues v. It returns false if the queue is full; it never blocks.
func (q *LFQueue) Push(v interface{}) bool {
	for {
		tail := atomic.LoadInt64(&q.tail)
		cell := &q.buf[tail&q.mask]
		seq := atomic.LoadInt64(&cell.seq)
		switch dif := seq - tail; {
		case dif == 0:
			if atomic.CompareAndSwapInt64(&q.tail, tail, tail+1) {
				cell.val = v
				atomic.StoreInt64(&cell.seq, tail+1)
				return true
			}
		case dif < 0:
			return false
		}
	}
}

// Pop dequeues the oldest element. It returns ok == false if the queue is
// empty; it never blocks.
func (q *LFQueue) Pop() (v interface{}, ok bool) {
	for {
		head := atomic.LoadInt64(&q.head)
		cell := &q.buf[head&q.mask]
		seq := atomic.LoadInt64(&cell.seq)
		switch dif := seq - (head + 1); {
		case dif == 0:
			if atomic.CompareAndSwapInt64(&q.head, head, head+1) {
				v = cell.val
				cell.val = nil // release the reference for the GC
				atomic.StoreInt64(&cell.seq, head+q.mask+1)
				return v, true
			}
		case dif < 0:
			return nil, false
		}
	}
}

// Len returns an approximate number of queued elements. It is only exact
// when no concurrent Push/Pop is in flight.
func (q *LFQueue) Len() int {
	n := atomic.LoadInt64(&q.tail) - atomic.LoadInt64(&q.head)
	if n < 0 {
		return 0
	}
	if n > int64(len(q.buf)) {
		return len(q.buf)
	}
	return int(n)
}
