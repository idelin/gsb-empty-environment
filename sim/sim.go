// Package sim is a deterministic, discrete-time, event-driven
// simulation framework. All randomness flows from a single seeded
// RNG and all events execute in (time, sequence) order, so a run is
// perfectly reproducible: same seed, same execution, same result.
package sim

import (
	"container/heap"
	"math/rand"
)

type event struct {
	time uint64
	seq  uint64
	fn   func(*Sim)
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

// Sim is a discrete-time event scheduler.
type Sim struct {
	now   uint64
	seq   uint64
	queue eventHeap
	rng   *rand.Rand
	Steps uint64
}

// New creates a simulator whose randomness derives from seed.
func New(seed int64) *Sim {
	return &Sim{rng: rand.New(rand.NewSource(seed))}
}

// Now returns the current logical time.
func (s *Sim) Now() uint64 { return s.now }

// RNG returns the simulation's deterministic random source.
func (s *Sim) RNG() *rand.Rand { return s.rng }

// Schedule enqueues fn to run delay time units from now.
func (s *Sim) Schedule(delay uint64, fn func(*Sim)) {
	s.seq++
	heap.Push(&s.queue, event{time: s.now + delay, seq: s.seq, fn: fn})
}

// Pending reports the number of queued events.
func (s *Sim) Pending() int { return len(s.queue) }

// Run executes events until the queue empties or maxSteps events
// have run. It returns the number of events executed.
func (s *Sim) Run(maxSteps uint64) uint64 {
	var ran uint64
	for s.queue.Len() > 0 && ran < maxSteps {
		e := heap.Pop(&s.queue).(event)
		s.now = e.time
		e.fn(s)
		ran++
	}
	s.Steps += ran
	return ran
}
