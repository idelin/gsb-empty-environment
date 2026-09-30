package streamsim

import "container/heap"

// event is a scheduled callback. order breaks ties so events at the same
// tick fire in FIFO order of their scheduling.
type event struct {
	tick  uint64
	order uint64
	fn    func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
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

// Simulator is a discrete-event scheduler over integer ticks.
type Simulator struct {
	now   uint64
	queue eventHeap
	order uint64
}

func NewSimulator() *Simulator { return &Simulator{} }

// Now returns the current tick.
func (s *Simulator) Now() uint64 { return s.now }

// At schedules fn to run at the given tick.
func (s *Simulator) At(tick uint64, fn func()) {
	if tick < s.now {
		panic("streamsim: cannot schedule in the past")
	}
	heap.Push(&s.queue, event{tick: tick, order: s.order, fn: fn})
	s.order++
}

// After schedules fn to run delay ticks from now.
func (s *Simulator) After(delay uint64, fn func()) { s.At(s.now+delay, fn) }

// Pending reports how many events remain.
func (s *Simulator) Pending() int { return len(s.queue) }

// Run processes events in tick order until the queue is empty.
func (s *Simulator) Run() {
	for len(s.queue) > 0 {
		e := heap.Pop(&s.queue).(event)
		s.now = e.tick
		e.fn()
	}
}
