// Package sim is a discrete-event network simulator. Time advances in
// integer ticks; packets, queues and links are pure in-memory events and no
// real network traffic is ever generated.
package sim

import "container/heap"

type event struct {
	tick uint64
	seq  uint64
	fn   func()
}

type evHeap []event

func (h evHeap) Len() int { return len(h) }
func (h evHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h evHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *evHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *evHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is the discrete-event scheduler.
type Sim struct {
	now uint64
	seq uint64
	h   evHeap
}

func New() *Sim { return &Sim{} }

// Now returns the current tick.
func (s *Sim) Now() uint64 { return s.now }

// At schedules fn at an absolute tick. Ties run in scheduling order (FIFO).
func (s *Sim) At(tick uint64, fn func()) {
	if tick < s.now {
		tick = s.now
	}
	s.seq++
	heap.Push(&s.h, event{tick: tick, seq: s.seq, fn: fn})
}

// After schedules fn delay ticks from now.
func (s *Sim) After(delay uint64, fn func()) { s.At(s.now+delay, fn) }

// Step runs the next event. Returns false when the queue is empty.
func (s *Sim) Step() bool {
	if len(s.h) == 0 {
		return false
	}
	e := heap.Pop(&s.h).(event)
	s.now = e.tick
	e.fn()
	return true
}

// Run processes events until the queue is empty or the next event is past
// the until tick.
func (s *Sim) Run(until uint64) {
	for len(s.h) > 0 && s.h[0].tick <= until {
		s.Step()
	}
}
