package sim

import "container/heap"

type event struct {
	tick  int64
	order int
	fn    func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(event))
}
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Queue is a discrete-event scheduler. Time advances in integer ticks only;
// no real network traffic or wall-clock timers are involved.
type Queue struct {
	h     eventHeap
	now   int64
	count int
}

// Now returns the current simulation tick.
func (q *Queue) Now() int64 { return q.now }

// Schedule runs fn after delay ticks (delay must be >= 0).
func (q *Queue) Schedule(delay int64, fn func()) {
	if delay < 0 {
		delay = 0
	}
	heap.Push(&q.h, event{tick: q.now + delay, order: q.count, fn: fn})
	q.count++
}

// Pending reports whether events remain.
func (q *Queue) Pending() bool { return len(q.h) > 0 }

// Run processes events in tick order until the queue is empty or maxTick
// is exceeded. Events at the same tick run in scheduling order.
func (q *Queue) Run(maxTick int64) {
	for len(q.h) > 0 {
		e := heap.Pop(&q.h).(event)
		if e.tick > maxTick {
			break
		}
		q.now = e.tick
		e.fn()
	}
}
