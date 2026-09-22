// Package raft implements a minimal Raft cluster running on a deterministic,
// single-process discrete-event simulator. Communication, failures (network
// partitions, crashes) and time (logical clock) are all controlled by the
// simulator, which makes safety and liveness properties testable without
// relying on wall-clock leases.
package raft

import "container/heap"

// Sim is a deterministic discrete-event simulator with a logical clock.
type Sim struct {
	now int64
	seq int64
	q   evHeap
}

type event struct {
	at  int64
	seq int64
	fn  func()
}

type evHeap []event

func (h evHeap) Len() int { return len(h) }
func (h evHeap) Less(i, j int) bool {
	return h[i].at < h[j].at || (h[i].at == h[j].at && h[i].seq < h[j].seq)
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

// NewSim returns an empty simulator at logical time 0.
func NewSim() *Sim { return &Sim{} }

// Now returns the current logical time.
func (s *Sim) Now() int64 { return s.now }

// After schedules fn to run d logical time units from now.
func (s *Sim) After(d int64, fn func()) {
	s.seq++
	heap.Push(&s.q, event{at: s.now + d, seq: s.seq, fn: fn})
}

// Step runs the next scheduled event. It reports whether an event ran.
func (s *Sim) Step() bool {
	if s.q.Len() == 0 {
		return false
	}
	e := heap.Pop(&s.q).(event)
	s.now = e.at
	e.fn()
	return true
}

// RunFor advances the logical clock by d, running every event scheduled in
// the window (now, now+d].
func (s *Sim) RunFor(d int64) {
	limit := s.now + d
	for s.q.Len() > 0 && s.q[0].at <= limit {
		s.Step()
	}
	s.now = limit
}
