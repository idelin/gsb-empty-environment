// Package sim implements a deterministic discrete-event simulator.
//
// The simulator owns a virtual clock that advances one tick at a time
// (Step). Events are scheduled at integer ticks and stored in a heap
// ordered by (time, sequence), so the execution order of events is a
// total, fully reproducible order for a given program.
package sim

import "container/heap"

type event struct {
	time uint64
	seq  uint64
	fn   func()
}

// eventHeap is a min-heap ordered by (time, seq). The monotonically
// increasing seq guarantees a stable, deterministic order between
// events scheduled for the same tick.
type eventHeap []event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) { *h = append(*h, x.(event)) }

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Simulator is a deterministic discrete-event scheduler with a virtual
// clock. All randomness must live outside the simulator (seeded RNGs)
// so that a run is 100% reproducible.
type Simulator struct {
	now   uint64
	seq   uint64
	queue eventHeap
}

// New returns an empty simulator with the clock at tick 0.
func New() *Simulator {
	s := &Simulator{}
	heap.Init(&s.queue)
	return s
}

// Now returns the current virtual tick.
func (s *Simulator) Now() uint64 { return s.now }

// After schedules fn to run delay ticks from now.
func (s *Simulator) After(delay uint64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{time: s.now + delay, seq: s.seq, fn: fn})
}

// Step advances the virtual clock by exactly one tick and executes
// every event scheduled at or before the new time. Events scheduled
// for the current tick while Step runs are executed in the same Step,
// in (time, seq) order.
func (s *Simulator) Step() {
	s.now++
	for len(s.queue) > 0 && s.queue[0].time <= s.now {
		ev := heap.Pop(&s.queue).(event)
		ev.fn()
	}
}

// Pending reports how many events are still queued.
func (s *Simulator) Pending() int { return len(s.queue) }
