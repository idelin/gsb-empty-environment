// Package scheduler implements a concurrency scheduler for a downloader.
// It admits jobs up to a configurable limit, queues the rest in FIFO order,
// and supports adjusting the limit at runtime.
//
// Semantics:
//   - Lowering the limit never interrupts running jobs; it only prevents
//     starting new ones while running >= limit.
//   - A limit of zero still allows Submit (queued), Cancel and Close.
//   - Queued tasks can be canceled; a canceled queued task never consumes
//     an admission slot ("cancel placeholder").
//   - Admission is strictly FIFO, so when the limit is raised again the
//     queue drains in submission order.
package scheduler

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrClosed is returned by Submit after Close, and reported by Wait for
	// tasks that were still queued when the scheduler was closed.
	ErrClosed = errors.New("scheduler: closed")
	// ErrCanceled is reported by Wait for tasks canceled while queued.
	ErrCanceled = errors.New("scheduler: task canceled")
)

type taskState int

const (
	stateQueued taskState = iota
	stateRunning
	stateFinished
	stateCanceled
)

// Task is a handle to a submitted job.
type Task struct {
	s    *Scheduler
	ctx  context.Context
	job  func(context.Context)
	done chan struct{} // closed when the task leaves the scheduler

	// Guarded by s.mu.
	state taskState
	err   error
}

// Scheduler admits jobs up to a runtime-adjustable concurrency limit.
type Scheduler struct {
	mu      sync.Mutex
	limit   int
	running int
	queue   []*Task // FIFO; canceled entries are removed eagerly
	closed  bool
}

// New returns a Scheduler admitting at most limit concurrent jobs.
// A negative limit is treated as zero.
func New(limit int) *Scheduler {
	if limit < 0 {
		limit = 0
	}
	return &Scheduler{limit: limit}
}

// SetLimit adjusts the concurrency limit at runtime. Lowering it does not
// interrupt running jobs; queued jobs only start while running < limit.
// Raising it immediately admits queued jobs in FIFO order.
func (s *Scheduler) SetLimit(n int) {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	s.limit = n
	s.dispatchLocked()
	s.mu.Unlock()
}

// Limit returns the current concurrency limit.
func (s *Scheduler) Limit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

// Running returns the number of jobs currently executing.
func (s *Scheduler) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Queued returns the number of tasks waiting for admission.
func (s *Scheduler) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Submit enqueues job and returns a handle immediately. The job runs in its
// own goroutine once admitted. Submit fails only if the scheduler is closed
// or ctx is already done.
func (s *Scheduler) Submit(ctx context.Context, job func(context.Context)) (*Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	t := &Task{s: s, ctx: ctx, job: job, done: make(chan struct{}), state: stateQueued}
	s.queue = append(s.queue, t)
	// Watch for context cancellation while queued. The watcher exits when
	// the task leaves the scheduler in any way.
	go func() {
		select {
		case <-ctx.Done():
			s.cancelQueued(t, ctx.Err())
		case <-t.done:
		}
	}()
	s.dispatchLocked()
	s.mu.Unlock()
	return t, nil
}

// Cancel removes a queued task so it never consumes an admission slot.
// It reports whether the task was still queued. Running tasks are never
// force-stopped; cancel their context instead.
func (t *Task) Cancel() bool {
	return t.s.cancelQueued(t, ErrCanceled)
}

func (s *Scheduler) cancelQueued(t *Task, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.state != stateQueued {
		return false
	}
	t.state = stateCanceled
	t.err = err
	close(t.done)
	for i, q := range s.queue {
		if q == t {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			break
		}
	}
	return true
}

// Wait blocks until the task finishes or is canceled and reports its
// terminal error: nil on success, ErrCanceled, ErrClosed, or the context
// error if it was canceled via context while queued.
func (t *Task) Wait() error {
	<-t.done
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	return t.err
}

// Done returns a channel closed when the task leaves the scheduler.
func (t *Task) Done() <-chan struct{} { return t.done }

// Close rejects new submissions and cancels every queued task with
// ErrClosed. Running tasks are left to finish naturally. Close is idempotent
// and safe to call with any limit, including zero.
func (s *Scheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	queued := s.queue
	s.queue = nil
	for _, t := range queued {
		if t.state == stateQueued {
			t.state = stateCanceled
			t.err = ErrClosed
			close(t.done)
		}
	}
}

// dispatchLocked admits queued tasks in FIFO order while running < limit.
// Canceled entries (removed eagerly by cancelQueued, but a racing ctx watcher
// may leave one) are skipped without consuming a slot.
func (s *Scheduler) dispatchLocked() {
	for s.running < s.limit && len(s.queue) > 0 {
		t := s.queue[0]
		s.queue = s.queue[1:]
		if t.state != stateQueued {
			continue
		}
		t.state = stateRunning
		s.running++
		go t.run()
	}
}

func (t *Task) run() {
	t.job(t.ctx)
	t.s.mu.Lock()
	t.s.running--
	t.state = stateFinished
	close(t.done)
	t.s.dispatchLocked()
	t.s.mu.Unlock()
}
