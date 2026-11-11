package session

import "sync"

// Scheduler serializes data-plane events. Every scheduled task runs to
// completion before the next task starts, and tasks are dispatched in the
// order they were enqueued. The deterministic SerialScheduler is suitable
// for tests; production code may supply a goroutine-backed implementation.
type Scheduler interface {
	Schedule(task func())
	Pending() int
	DispatchOne() bool
	DispatchAll()
}

// SerialScheduler is a manually driven, FIFO Scheduler. Nothing runs until
// DispatchOne or DispatchAll is called, which gives tests full control over
// the linear ordering of events.
type SerialScheduler struct {
	mu sync.Mutex
	q  []func()
}

// NewSerialScheduler creates an empty serial scheduler.
func NewSerialScheduler() *SerialScheduler {
	return &SerialScheduler{}
}

// Schedule enqueues a task behind any previously scheduled tasks.
func (s *SerialScheduler) Schedule(task func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.q = append(s.q, task)
}

// Pending reports how many tasks are queued for dispatch.
func (s *SerialScheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.q)
}

// DispatchOne runs the oldest queued task. It returns false when the queue
// is empty.
func (s *SerialScheduler) DispatchOne() bool {
	s.mu.Lock()
	if len(s.q) == 0 {
		s.mu.Unlock()
		return false
	}
	task := s.q[0]
	s.q = s.q[1:]
	s.mu.Unlock()

	task()
	return true
}

// DispatchAll runs every queued task in enqueue order.
func (s *SerialScheduler) DispatchAll() {
	for s.DispatchOne() {
	}
}
