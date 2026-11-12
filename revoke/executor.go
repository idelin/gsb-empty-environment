package revoke

import "sync"

// goroutineExecutor runs every admitted task on its own goroutine. It is the
// production executor for hosts that want full concurrency.
type goroutineExecutor struct{}

// NewGoroutineExecutor returns an Executor backed by unbounded goroutines.
func NewGoroutineExecutor() Executor {
	return goroutineExecutor{}
}

func (goroutineExecutor) Go(fn func()) {
	go fn()
}

// PausableExecutor is an Executor whose task starts are fully controlled by the
// test driver. While paused, submitted tasks queue without running; Step starts
// queued tasks one at a time and Resume drains the queue.
//
// Deterministic drivers keep at most one task active at a time, so callback
// admission and logout barriers interleave in one reproducible order. A task
// blocked inside its callback still counts as active, which is what a logout
// barrier waits on.
type PausableExecutor struct {
	mu      sync.Mutex
	paused  bool
	closed  bool
	pending []func()
	active  int
	started int
	done    int
	idle    *sync.Cond
	stop    chan struct{}
}

// NewPausableExecutor creates a PausableExecutor that starts paused.
func NewPausableExecutor() *PausableExecutor {
	pe := &PausableExecutor{paused: true, stop: make(chan struct{})}
	pe.idle = sync.NewCond(&pe.mu)
	return pe
}

// Go enqueues a task, starting it immediately when the executor is not paused.
func (pe *PausableExecutor) Go(fn func()) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.closed {
		return
	}
	pe.pending = append(pe.pending, fn)
	if !pe.paused {
		pe.pumpLocked()
	}
}

// spawnLocked runs task on a fresh goroutine.
func (pe *PausableExecutor) spawnLocked(task func()) {
	pe.active++
	pe.started++
	go func() {
		select {
		case <-pe.stop:
		default:
			task()
		}

		pe.mu.Lock()
		pe.active--
		pe.done++
		if pe.active == 0 {
			pe.idle.Broadcast()
		}
		pe.mu.Unlock()
	}()
}

// pumpLocked drains queued tasks while the executor is running.
func (pe *PausableExecutor) pumpLocked() {
	for !pe.paused && len(pe.pending) > 0 {
		task := pe.pending[0]
		pe.pending = pe.pending[1:]
		pe.spawnLocked(task)
	}
}

// Pause stops future task starts; running tasks are unaffected.
func (pe *PausableExecutor) Pause() {
	pe.mu.Lock()
	pe.paused = true
	pe.mu.Unlock()
}

// Resume drains the queued backlog.
func (pe *PausableExecutor) Resume() {
	pe.mu.Lock()
	pe.paused = false
	pe.pumpLocked()
	pe.mu.Unlock()
}

// Step starts exactly one queued task on its own goroutine. It must be called
// while paused and only when no earlier task is active, so the stepped task
// reaches its dispatcher admission decision immediately. Callers use
// Receipt.WaitScheduled to observe that settled linearization point.
func (pe *PausableExecutor) Step(n int) int {
	pe.mu.Lock()
	if n < 1 || len(pe.pending) == 0 {
		pe.mu.Unlock()
		return 0
	}
	task := pe.pending[0]
	pe.pending = pe.pending[1:]
	pe.spawnLocked(task)
	pe.mu.Unlock()
	return 1
}

// WaitDone blocks until every started task has finished.
func (pe *PausableExecutor) WaitDone() {
	pe.mu.Lock()
	for pe.active > 0 {
		pe.idle.Wait()
	}
	pe.mu.Unlock()
}

// Started reports how many tasks have ever been started.
func (pe *PausableExecutor) Started() int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.started
}

// Done reports how many started tasks have finished.
func (pe *PausableExecutor) Done() int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.done
}

// Active reports how many started tasks are still running.
func (pe *PausableExecutor) Active() int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.active
}

// Queued reports how many tasks are waiting to start.
func (pe *PausableExecutor) Queued() int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return len(pe.pending)
}

// Close shuts the executor down. Tasks still queued at Close are discarded and
// running tasks are not interrupted.
func (pe *PausableExecutor) Close() {
	pe.mu.Lock()
	if pe.closed {
		pe.mu.Unlock()
		return
	}
	pe.closed = true
	pe.pending = nil
	stop := pe.stop
	pe.mu.Unlock()
	close(stop)
}
