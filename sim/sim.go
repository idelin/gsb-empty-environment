// Package sim is a single-process, single-goroutine discrete-event simulator.
//
// Wall-clock time, communication and faults are all modelled as scheduled
// events, so every run is deterministic and reproducible. Nothing in this
// package (or its callers) starts an OS goroutine: "concurrency" between
// proxy branches is expressed purely as events on the queue.
package sim

import (
	"container/heap"
)

// Action is a unit of work executed at a given virtual time.
type Action func(s *Sim)

type event struct {
	id  uint64 // tie-breaker: insertion order, giving FIFO within an instant
	at  int64  // virtual time in nanoseconds
	fn  Action
	idx int
}

type eventQueue []*event

func (q eventQueue) Len() int { return len(q) }

func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].id < q[j].id
}

func (q eventQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].idx = i
	q[j].idx = j
}

func (q *eventQueue) Push(x any) {
	ev := x.(*event)
	ev.idx = len(*q)
	*q = append(*q, ev)
}

func (q *eventQueue) Pop() any {
	old := *q
	n := len(old)
	ev := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return ev
}

// Sim is the deterministic virtual machine that owns the clock and queue.
type Sim struct {
	now    int64
	queue  eventQueue
	seq    uint64
	maxRun int
	// NextID hands out process-wide unique ids (attempt ids, request ids).
	idCounter uint64
}

// New builds a simulator that refuses to execute more than maxEvents actions
// in one Run: a runaway retry loop therefore fails loudly instead of hanging.
func New(maxEvents int) *Sim {
	s := &Sim{maxRun: maxEvents}
	heap.Init(&s.queue)
	return s
}

// Now returns the current virtual time.
func (s *Sim) Now() int64 { return s.now }

// NextID returns a fresh, process-wide unique, monotonically increasing id.
func (s *Sim) NextID() uint64 { s.idCounter++; return s.idCounter }

// Schedule places fn on the queue at absolute virtual time at.
// Zero-delay callbacks are still deferred to the queue, so callbacks never
// re-enter their callers synchronously.
func (s *Sim) Schedule(at int64, fn Action) {
	if at < s.now {
		at = s.now
	}
	heap.Push(&s.queue, &event{id: s.seq, at: at, fn: fn})
	s.seq++
}

// After schedules fn after delay d relative to the current virtual time.
func (s *Sim) After(d int64, fn Action) { s.Schedule(s.now+d, fn) }

// Run drains the queue in virtual-time order until empty.
func (s *Sim) Run() {
	for n := 0; len(s.queue) > 0; n++ {
		if n >= s.maxRun {
			panic("sim: event budget exhausted (possible retry storm)")
		}
		ev := heap.Pop(&s.queue).(*event)
		s.now = ev.at
		ev.fn(s)
	}
}
