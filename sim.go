// Package sim implements a single-process event simulator with virtual time,
// a fault-injecting network, replica nodes, and a read path that schedules
// read-repairs through a per-key coalescing, globally concurrency-limited
// repair scheduler with bounded retries.
package sim

import "container/heap"

type event struct {
	time int64
	seq  uint64
	fn   func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
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

// Sim is a discrete-event simulator. All communication, faults and time are
// controlled here: nothing uses the wall clock, goroutines or real sockets.
type Sim struct {
	now  int64
	seq  uint64
	heap eventHeap
	logf func(format string, args ...any)
	// AfterEach, when set, runs after every processed event. Tests use it to
	// assert invariants (safety evidence) at every simulated instant.
	AfterEach func()
}

func New() *Sim { return &Sim{} }

// Now returns the current virtual time in ticks.
func (s *Sim) Now() int64 { return s.now }

// SetLogger installs a logger receiving timestamped simulator events.
func (s *Sim) SetLogger(f func(format string, args ...any)) { s.logf = f }

// Logf emits a timestamped log line if a logger is installed.
func (s *Sim) Logf(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// After schedules fn to run d ticks from now. Ties break by insertion order.
func (s *Sim) After(d int64, fn func()) {
	s.seq++
	heap.Push(&s.heap, event{time: s.now + d, seq: s.seq, fn: fn})
}

// Step processes the next event. It returns false when no events remain.
func (s *Sim) Step() bool {
	if len(s.heap) == 0 {
		return false
	}
	e := heap.Pop(&s.heap).(event)
	s.now = e.time
	e.fn()
	if s.AfterEach != nil {
		s.AfterEach()
	}
	return true
}

// Run processes events until the queue is empty.
func (s *Sim) Run() {
	for s.Step() {
	}
}

// Pending reports how many events are still queued.
func (s *Sim) Pending() int { return len(s.heap) }
