// Package tcpsim implements a discrete-event simulation of a TCP-style
// congestion-controlled flow. All packets, queues and links run on an
// integer-tick event engine; no real network traffic is ever generated.
package tcpsim

import "container/heap"

// event is a scheduled callback at an integer tick. seq breaks ties so
// that events scheduled for the same tick run in scheduling order.
type event struct {
	tick int
	seq  int
	fn   func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
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

// Engine is a discrete-event scheduler driven by integer ticks.
type Engine struct {
	now int
	seq int
	q   eventHeap
}

// NewEngine returns an empty engine at tick 0.
func NewEngine() *Engine {
	e := &Engine{}
	heap.Init(&e.q)
	return e
}

// Now returns the current tick.
func (e *Engine) Now() int { return e.now }

// After schedules fn to run delay ticks from now. A negative delay is
// treated as zero.
func (e *Engine) After(delay int, fn func()) {
	if delay < 0 {
		delay = 0
	}
	e.seq++
	heap.Push(&e.q, event{tick: e.now + delay, seq: e.seq, fn: fn})
}

// Pending reports how many events are still queued.
func (e *Engine) Pending() int { return e.q.Len() }

// Run processes events in tick order until the queue is empty.
func (e *Engine) Run() {
	for e.q.Len() > 0 {
		e.step()
	}
}

// RunUntil processes events up to and including the given tick, then
// advances the clock to that tick.
func (e *Engine) RunUntil(tick int) {
	for e.q.Len() > 0 && e.q[0].tick <= tick {
		e.step()
	}
	if tick > e.now {
		e.now = tick
	}
}

func (e *Engine) step() {
	ev := heap.Pop(&e.q).(event)
	e.now = ev.tick
	ev.fn()
}
