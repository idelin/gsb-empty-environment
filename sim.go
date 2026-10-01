package dvsim

import "container/heap"

// Event is a single discrete event scheduled at an integer tick.
type Event struct {
	Tick int
	Seq  int64
	Fn   func()
}

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].Tick != h[j].Tick {
		return h[i].Tick < h[j].Tick
	}
	return h[i].Seq < h[j].Seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Simulator is a discrete-event scheduler. Time advances in integer ticks.
// No real network traffic exists: everything is an in-memory event.
type Simulator struct {
	now    int
	seq    int64
	events eventHeap
}

func NewSimulator() *Simulator {
	return &Simulator{}
}

// Now returns the current tick.
func (s *Simulator) Now() int { return s.now }

// Schedule runs fn at tick now+delay (delay is clamped to >= 0).
func (s *Simulator) Schedule(delay int, fn func()) {
	if delay < 0 {
		delay = 0
	}
	s.seq++
	heap.Push(&s.events, Event{Tick: s.now + delay, Seq: s.seq, Fn: fn})
}

// Pending reports the number of scheduled events.
func (s *Simulator) Pending() int { return len(s.events) }

// Run processes events in tick order until no event remains at a tick
// <= untilTick. Events scheduled beyond untilTick stay queued.
func (s *Simulator) Run(untilTick int) {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(Event)
		if e.Tick > untilTick {
			heap.Push(&s.events, e)
			return
		}
		s.now = e.Tick
		e.Fn()
	}
}
