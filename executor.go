// Package executor provides a worker-pool task executor with a timed,
// two-phase shutdown: first drain, then request cancellation and report
// the tasks that never finished.
//
// Cancellation in Go is cooperative: goroutines cannot be force-killed.
// Tasks that ignore their context keep running after Shutdown returns, so
// the executor keeps a safe, mutex-protected result path that accepts
// "late" results at any time. The shutdown report is a live view: it can
// be queried repeatedly and never lists a completed task as unfinished.
package executor

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrShuttingDown is returned by Submit once shutdown has begun.
	ErrShuttingDown = errors.New("executor: shutting down, not accepting new tasks")
	// ErrDuplicateTask is returned by Submit for an already-used task ID.
	ErrDuplicateTask = errors.New("executor: duplicate task ID")
)

// TaskID uniquely identifies a submitted task.
type TaskID string

// TaskFunc is the unit of work. The ctx is canceled when Shutdown's
// deadline expires; well-behaved tasks should abort on ctx.Done().
type TaskFunc func(ctx context.Context) (interface{}, error)

// TaskResult is the recorded outcome of a finished task.
type TaskResult struct {
	ID    TaskID
	Value interface{}
	Err   error
	// Late is true when the task finished after Shutdown had already
	// returned (e.g. it ignored cancellation).
	Late bool
}

// Report is a point-in-time snapshot of the executor's shutdown state.
// It is safe to query repeatedly via Report(); late completions move
// tasks from Unfinished to Completed between queries.
type Report struct {
	// ShutdownStarted is true once Shutdown has been initiated.
	ShutdownStarted bool
	// ShutdownReturned is true once Shutdown has returned.
	ShutdownReturned bool
	// Drained is true when every task finished before the deadline and
	// no cancellation had to be requested.
	Drained bool
	// Completed lists finished task IDs (sorted).
	Completed []TaskID
	// Unfinished lists task IDs not yet finished (sorted): still queued
	// or still running. A task never appears in both lists.
	Unfinished []TaskID
	// LateCompletions counts tasks that finished after Shutdown returned.
	LateCompletions int
}

type taskState int

const (
	stateQueued taskState = iota
	stateRunning
	stateCompleted
)

// Executor runs submitted tasks on a fixed pool of worker goroutines.
type Executor struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []TaskID
	funcs    map[TaskID]TaskFunc
	states   map[TaskID]taskState
	results  map[TaskID]TaskResult
	running  int
	draining bool // set when Shutdown begins: no new submissions
	finished bool // set when Shutdown returns
	drained  bool
	late     int
	wg       sync.WaitGroup
}

// New creates an Executor with the given number of worker goroutines.
func New(workers int) *Executor {
	if workers < 1 {
		workers = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Executor{
		ctx:     ctx,
		cancel:  cancel,
		funcs:   make(map[TaskID]TaskFunc),
		states:  make(map[TaskID]taskState),
		results: make(map[TaskID]TaskResult),
	}
	e.cond = sync.NewCond(&e.mu)
	e.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go e.worker()
	}
	return e
}

// Submit enqueues a task. It fails once shutdown has begun or when the
// ID is already in use.
func (e *Executor) Submit(id TaskID, fn TaskFunc) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.draining {
		return ErrShuttingDown
	}
	if _, exists := e.states[id]; exists {
		return ErrDuplicateTask
	}
	e.states[id] = stateQueued
	e.funcs[id] = fn
	e.queue = append(e.queue, id)
	e.cond.Signal()
	return nil
}

// worker pulls tasks until the executor is draining and the queue is
// empty. Result recording stays mutex-protected forever, so a task that
// ignores cancellation can still deliver its result safely after
// Shutdown has returned.
func (e *Executor) worker() {
	defer e.wg.Done()
	for {
		e.mu.Lock()
		for len(e.queue) == 0 && !e.draining {
			e.cond.Wait()
		}
		if len(e.queue) == 0 {
			// Draining with an empty queue: exit.
			e.mu.Unlock()
			return
		}
		id := e.queue[0]
		e.queue = e.queue[1:]
		fn := e.funcs[id]
		e.states[id] = stateRunning
		e.running++
		e.mu.Unlock()

		value, err := fn(e.ctx)

		e.mu.Lock()
		e.running--
		e.states[id] = stateCompleted
		late := e.finished
		e.results[id] = TaskResult{ID: id, Value: value, Err: err, Late: late}
		if late {
			e.late++
		}
		e.cond.Broadcast()
		e.mu.Unlock()
	}
}

// inflightLocked counts queued plus running tasks. Caller holds mu.
func (e *Executor) inflightLocked() int {
	return len(e.queue) + e.running
}

// Shutdown initiates a timed, two-phase shutdown and returns a snapshot
// report. Phase one stops accepting submissions and drains queued and
// running tasks until the timeout elapses. Phase two requests
// cancellation of the remaining tasks via their context, drops the
// never-started queue, and returns them as unfinished.
//
// Tasks that ignore cancellation are not force-killed (impossible in
// Go); their late results are still recorded safely and remain visible
// through Result and subsequent Report calls.
func (e *Executor) Shutdown(timeout time.Duration) Report {
	e.mu.Lock()
	if e.finished {
		r := e.reportLocked()
		e.mu.Unlock()
		return r
	}
	if !e.draining {
		e.draining = true
		e.cond.Broadcast()
	}
	e.mu.Unlock()

	// Watcher closes drainedCh once nothing is in flight. It also
	// exits when Shutdown finishes, so it never leaks on behalf of
	// tasks that ignore cancellation forever.
	drainedCh := make(chan struct{})
	go func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		for !e.finished && e.inflightLocked() > 0 {
			e.cond.Wait()
		}
		if e.inflightLocked() == 0 {
			close(drainedCh)
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-drainedCh:
	case <-timer.C:
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.finished = true
	if e.inflightLocked() == 0 {
		e.drained = true
	} else {
		// Deadline expired: request cooperative cancellation and
		// abandon the never-started queue. Running tasks keep their
		// safe result path.
		e.cancel()
		e.queue = nil
	}
	e.cond.Broadcast()
	return e.reportLocked()
}

// Report returns the current shutdown snapshot. It is safe to call at
// any time and any number of times; late completions are reflected in
// every subsequent call.
func (e *Executor) Report() Report {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reportLocked()
}

// reportLocked builds the snapshot. Caller holds mu.
func (e *Executor) reportLocked() Report {
	r := Report{
		ShutdownStarted:  e.draining,
		ShutdownReturned: e.finished,
		Drained:          e.drained,
		LateCompletions:  e.late,
	}
	for id, st := range e.states {
		if st == stateCompleted {
			r.Completed = append(r.Completed, id)
		} else {
			r.Unfinished = append(r.Unfinished, id)
		}
	}
	sort.Slice(r.Completed, func(i, j int) bool { return r.Completed[i] < r.Completed[j] })
	sort.Slice(r.Unfinished, func(i, j int) bool { return r.Unfinished[i] < r.Unfinished[j] })
	return r
}

// Result returns the recorded outcome of a finished task, including
// results that arrived late, after Shutdown returned.
func (e *Executor) Result(id TaskID) (TaskResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res, ok := e.results[id]
	return res, ok
}

// Wait blocks until every worker goroutine has exited, i.e. until all
// tasks — including ones that ignored cancellation — have finished.
func (e *Executor) Wait() {
	e.wg.Wait()
}
