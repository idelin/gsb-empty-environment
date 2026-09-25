// Package snaprepl implements log replication with chunked snapshot
// installation on top of a single-process discrete-event simulator.
// Communication, failures and time are all controlled by the simulator.
package snaprepl

import "container/heap"

type event struct {
	time int64
	seq  int
	run  func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is a discrete-event simulator. The virtual clock only advances
// when an event is processed, so every run is fully deterministic.
type Sim struct {
	now int64
	seq int
	h   eventHeap
}

func NewSim() *Sim { return &Sim{} }

func (s *Sim) Now() int64 { return s.now }

// After schedules f to run d virtual time units from now.
func (s *Sim) After(d int64, f func()) {
	heap.Push(&s.h, event{time: s.now + d, seq: s.seq, run: f})
	s.seq++
}

// Step processes the next event. It returns false when the queue is empty.
func (s *Sim) Step() bool {
	if len(s.h) == 0 {
		return false
	}
	e := heap.Pop(&s.h).(event)
	s.now = e.time
	e.run()
	return true
}

// RunUntil processes events until the virtual clock reaches limit.
func (s *Sim) RunUntil(limit int64) {
	for len(s.h) > 0 && s.h[0].time <= limit {
		s.Step()
	}
	if s.now < limit {
		s.now = limit
	}
}
