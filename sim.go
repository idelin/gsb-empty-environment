// Package sim simulates link-state routing (LSA flooding, SPF
// recomputation) over a discrete-event network model. All packets,
// per-link FIFO queues and links run on an integer-tick event
// scheduler; no real network traffic is generated.
package sim

import "container/heap"

type event struct {
	tick   int
	order  int
	action func()
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
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Simulator is a discrete-event scheduler driven by integer ticks.
type Simulator struct {
	now    int
	events eventHeap
	order  int

	EventsProcessed int
}

func NewSimulator() *Simulator { return &Simulator{} }

// Now returns the current simulation tick.
func (s *Simulator) Now() int { return s.now }

// After schedules action to run at tick now+delay.
func (s *Simulator) After(delay int, action func()) {
	if delay < 0 {
		panic("sim: negative delay")
	}
	heap.Push(&s.events, event{tick: s.now + delay, order: s.order, action: action})
	s.order++
}

// Pending reports how many events are still queued.
func (s *Simulator) Pending() int { return len(s.events) }

// Run processes events in tick order until the queue is empty or the
// next event lies beyond maxTick.
func (s *Simulator) Run(maxTick int) {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		if e.tick > maxTick {
			heap.Push(&s.events, e)
			return
		}
		s.now = e.tick
		e.action()
		s.EventsProcessed++
	}
}
