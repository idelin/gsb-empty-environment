package sim

import "container/heap"

// Event is a single discrete event scheduled at an integer tick.
type Event struct {
	tick  uint64
	order uint64
	fn    func()
}

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
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

// Scheduler is a discrete-event scheduler driven by integer ticks.
// Events at the same tick run in FIFO order of their scheduling.
type Scheduler struct {
	now     uint64
	counter uint64
	h       eventHeap
}

// NewScheduler returns an empty scheduler at tick 0.
func NewScheduler() *Scheduler { return &Scheduler{} }

// Now returns the current tick.
func (s *Scheduler) Now() uint64 { return s.now }

// At schedules fn to run at the given tick.
func (s *Scheduler) At(tick uint64, fn func()) {
	if tick < s.now {
		tick = s.now
	}
	heap.Push(&s.h, Event{tick: tick, order: s.counter, fn: fn})
	s.counter++
}

// After schedules fn to run delay ticks from now.
func (s *Scheduler) After(delay uint64, fn func()) { s.At(s.now+delay, fn) }

// Pending reports the number of queued events.
func (s *Scheduler) Pending() int { return len(s.h) }

// Run processes events in tick order until the queue is empty or the next
// event lies beyond maxTick.
func (s *Scheduler) Run(maxTick uint64) {
	for len(s.h) > 0 {
		e := heap.Pop(&s.h).(Event)
		if e.tick > maxTick {
			heap.Push(&s.h, e)
			return
		}
		s.now = e.tick
		e.fn()
	}
}
