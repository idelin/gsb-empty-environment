// Package worksteal implements a work-stealing scheduler: each worker owns a
// local deque, idle workers steal tasks from the head of other workers'
// deques, and every admitted task is executed exactly once.
package worksteal

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClosed is returned by Submit when the scheduler is shutting down.
var ErrClosed = errors.New("worksteal: scheduler closed")

// Task is a unit of work. It receives the Worker executing it so it can
// spawn subtasks into that worker's local deque via Worker.Submit.
// A non-nil return value (or a panic) marks the task as failed.
type Task func(w *Worker) error

// Stats is a point-in-time snapshot of scheduler counters.
// Conservation invariant once Close returns: Submitted == Completed + Failed.
type Stats struct {
	Submitted int64
	Completed int64
	Failed    int64
	Stolen    int64
}

// Scheduler owns a fixed set of workers and their deques.
type Scheduler struct {
	workers []*Worker
	stop    chan struct{}
	wg      sync.WaitGroup // worker goroutines

	// Lifecycle state. pending counts admitted tasks not yet finished;
	// it is guarded by mu together with closed so that Close cannot miss
	// a concurrent submission (no WaitGroup add-after-zero race).
	mu      sync.Mutex
	cond    sync.Cond
	pending int
	closed  bool

	rr atomic.Uint64 // round-robin cursor for external Submit

	submitted atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	stolen    atomic.Int64
}

// New creates a scheduler with the given number of workers (at least 1)
// and starts their goroutines.
func New(numWorkers int) *Scheduler {
	if numWorkers < 1 {
		numWorkers = 1
	}
	s := &Scheduler{stop: make(chan struct{})}
	s.cond.L = &s.mu
	s.workers = make([]*Worker, numWorkers)
	for i := range s.workers {
		s.workers[i] = &Worker{id: i, sched: s}
	}
	s.wg.Add(numWorkers)
	for _, w := range s.workers {
		go w.run()
	}
	return s
}

// admit reserves a pending slot, or reports that the scheduler is closed.
func (s *Scheduler) admit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.pending++
	return true
}

// Submit enqueues a task from outside any worker, round-robin across deques.
// It returns ErrClosed if the scheduler is shutting down.
func (s *Scheduler) Submit(t Task) error {
	if !s.admit() {
		return ErrClosed
	}
	s.submitted.Add(1)
	idx := int(s.rr.Add(1)-1) % len(s.workers)
	s.workers[idx].deque.push(t)
	return nil
}

// Stats returns a snapshot of the counters.
func (s *Scheduler) Stats() Stats {
	return Stats{
		Submitted: s.submitted.Load(),
		Completed: s.completed.Load(),
		Failed:    s.failed.Load(),
		Stolen:    s.stolen.Load(),
	}
}

// Close stops accepting new tasks, waits until every admitted task has
// finished (including subtasks spawned during the wait), then stops the
// workers. After Close returns, Submitted == Completed + Failed.
func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	for s.pending > 0 {
		s.cond.Wait()
	}
	s.mu.Unlock()
	close(s.stop)
	s.wg.Wait()
}

// finish records the outcome of an executed task and releases its slot.
func (s *Scheduler) finish(err error) {
	if err != nil {
		s.failed.Add(1)
	} else {
		s.completed.Add(1)
	}
	s.mu.Lock()
	s.pending--
	if s.pending == 0 {
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// steal takes the oldest task from another worker's deque, or returns nil.
func (s *Scheduler) steal(self int) Task {
	n := len(s.workers)
	for i := 1; i < n; i++ {
		victim := s.workers[(self+i)%n]
		if t := victim.deque.steal(); t != nil {
			s.stolen.Add(1)
			return t
		}
	}
	return nil
}

// Worker executes tasks from its own deque (LIFO) and steals from others
// (FIFO) when idle.
type Worker struct {
	id    int
	sched *Scheduler
	deque deque
}

// ID returns the worker's index within the scheduler.
func (w *Worker) ID() int { return w.id }

// Submit enqueues a subtask into this worker's own deque, so a busy worker
// keeps locality while idle peers can steal from the head of the deque.
// It returns ErrClosed if the scheduler is shutting down.
func (w *Worker) Submit(t Task) error {
	s := w.sched
	if !s.admit() {
		return ErrClosed
	}
	s.submitted.Add(1)
	w.deque.push(t)
	return nil
}

func (w *Worker) run() {
	s := w.sched
	defer s.wg.Done()
	for {
		if t := w.deque.pop(); t != nil {
			w.execute(t)
			continue
		}
		if t := s.steal(w.id); t != nil {
			w.execute(t)
			continue
		}
		select {
		case <-s.stop:
			return
		case <-time.After(200 * time.Microsecond):
		}
	}
}

// execute runs one task, converting panics into failures so the pending
// count and statistics stay conserved even for misbehaving tasks.
func (w *Worker) execute(t Task) {
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					err = e
				} else {
					err = errTaskPanicked
				}
			}
		}()
		err = t(w)
	}()
	w.sched.finish(err)
}

var errTaskPanicked = errors.New("worksteal: task panicked")
