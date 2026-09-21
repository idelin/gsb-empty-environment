// Package worksteal implements a work-stealing scheduler.
//
// Each worker owns a local deque. A worker pops tasks from the bottom of
// its own deque; idle workers steal from the top of other workers' deques.
// Every task is executed at most once. Tasks may spawn subtasks, which are
// pushed onto the spawning worker's deque so peers can steal and share the
// load. Shutdown waits for the outstanding-task count to drain to zero and
// guarantees statistical conservation: submitted == completed + failed.
package worksteal

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Task is a unit of work. It receives the Scope of the executing worker so
// it can spawn subtasks. A non-nil return marks the task as failed; failed
// tasks are counted, never retried (at-most-once semantics).
type Task func(s *Scope) error

// Stats is a point-in-time snapshot of scheduler counters.
type Stats struct {
	Submitted int64
	Completed int64
	Failed    int64
}

// Conserved reports whether Submitted == Completed + Failed.
func (st Stats) Conserved() bool { return st.Submitted == st.Completed+st.Failed }

// Scheduler runs tasks across a fixed set of workers.
type Scheduler struct {
	lanes   []deque
	pending sync.WaitGroup // outstanding (queued or running) tasks
	workers sync.WaitGroup // worker goroutines

	stop     chan struct{}
	stopOnce sync.Once
	wake     chan struct{}

	rr atomic.Uint64 // round-robin cursor for external submits

	submitted atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
}

// New creates a Scheduler with the given number of workers (>= 1).
func New(workers int) *Scheduler {
	if workers < 1 {
		workers = 1
	}
	s := &Scheduler{
		lanes: make([]deque, workers),
		stop:  make(chan struct{}),
		wake:  make(chan struct{}, workers),
	}
	return s
}

// Workers returns the number of worker goroutines.
func (s *Scheduler) Workers() int { return len(s.lanes) }

// Start launches the worker goroutines.
func (s *Scheduler) Start() {
	for i := range s.lanes {
		s.workers.Add(1)
		go s.worker(i)
	}
}

// Submit enqueues a top-level task from outside any worker. It is safe for
// concurrent use, but must not be called concurrently with Wait or Shutdown
// once the outstanding count may have reached zero.
func (s *Scheduler) Submit(t Task) {
	lane := int(s.rr.Add(1)-1) % len(s.lanes)
	s.enqueue(lane, t)
}

// enqueue records a new task and pushes it onto the given lane.
// The pending.Add(1) happens before the task becomes visible to any worker,
// so the outstanding count can never hit zero while a queued task exists.
func (s *Scheduler) enqueue(lane int, t Task) {
	s.submitted.Add(1)
	s.pending.Add(1)
	s.lanes[lane].pushBottom(t)
	s.signal()
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Scope is the per-worker handle passed to every Task. Subtasks submitted
// through it land on the local deque of the executing worker, where peers
// can steal them.
type Scope struct {
	sched *Scheduler
	lane  int
}

// Submit spawns a subtask onto the executing worker's local deque.
// It must be called before the parent task returns: because the parent is
// still counted as outstanding, pending.Add(1) here can never race with the
// count reaching zero, which is what makes resubmission during shutdown safe.
func (sc *Scope) Submit(t Task) { sc.sched.enqueue(sc.lane, t) }

// Lane reports the index of the executing worker (useful for tests).
func (sc *Scope) Lane() int { return sc.lane }

func (s *Scheduler) worker(id int) {
	defer s.workers.Done()
	scope := &Scope{sched: s, lane: id}
	for {
		if t, ok := s.nextTask(id); ok {
			s.execute(scope, t)
			continue
		}
		select {
		case <-s.stop:
			return
		case <-s.wake:
		case <-time.After(time.Millisecond):
		}
	}
}

// nextTask tries the local deque first, then steals from peers, starting at
// a rotating offset so thieves do not all hammer the same victim.
func (s *Scheduler) nextTask(id int) (Task, bool) {
	if t, ok := s.lanes[id].popBottom(); ok {
		return t, true
	}
	n := len(s.lanes)
	start := int(s.rr.Add(1)) % n
	for i := 0; i < n; i++ {
		victim := (start + i) % n
		if victim == id {
			continue
		}
		if t, ok := s.lanes[victim].stealTop(); ok {
			return t, true
		}
	}
	return nil, false
}

func (s *Scheduler) execute(scope *Scope, t Task) {
	defer s.pending.Done()
	if err := t(scope); err != nil {
		s.failed.Add(1)
	} else {
		s.completed.Add(1)
	}
}

// Wait blocks until every submitted task (including subtasks) has finished.
func (s *Scheduler) Wait() { s.pending.Wait() }

// Shutdown waits for all outstanding tasks to finish, then stops the
// workers. It is idempotent. After Shutdown returns, Stats is conserved.
func (s *Scheduler) Shutdown() {
	s.Wait()
	s.stopOnce.Do(func() { close(s.stop) })
	s.workers.Wait()
}

// Stats returns a snapshot of the counters.
func (s *Scheduler) Stats() Stats {
	return Stats{
		Submitted: s.submitted.Load(),
		Completed: s.completed.Load(),
		Failed:    s.failed.Load(),
	}
}

// ErrInjected is a convenience error tests and demos can return to mark a
// task as failed.
var ErrInjected = errors.New("injected failure")
