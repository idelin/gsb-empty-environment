// Package scheduler implements a concurrency scheduler for a downloader.
// The concurrency limit can be adjusted at runtime: lowering the limit never
// interrupts tasks that are already running, it only blocks new grants until
// the number of running tasks drops below the new limit. A limit of zero
// still allows cancellation and Close. Waiters are served in FIFO order.
package scheduler

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Acquire after the scheduler has been closed.
var ErrClosed = errors.New("scheduler: closed")

// Scheduler hands out a bounded number of execution slots. The bound may be
// changed at any time via SetLimit.
type Scheduler struct {
	mu      sync.Mutex
	limit   int
	running int
	queue   []*request
	closed  bool
}

// request is a queued waiter. granted/err are written under Scheduler.mu
// before ch is closed, so receiving from ch synchronizes with those writes.
type request struct {
	granted bool
	err     error
	ch      chan struct{}
}

// Slot represents one granted unit of concurrency. Release must be called
// when the task finishes; it is safe to call Release more than once.
type Slot struct {
	sched *Scheduler
	once  sync.Once
}

// New returns a Scheduler that allows up to limit concurrent slots.
// A negative limit is treated as zero.
func New(limit int) *Scheduler {
	if limit < 0 {
		limit = 0
	}
	return &Scheduler{limit: limit}
}

// Acquire blocks until a slot is granted, the context is canceled, or the
// scheduler is closed. Waiters are queued and granted slots in FIFO order.
// A canceled waiter never consumes a slot: if cancellation races with a
// grant, the slot is handed back to the queue.
func (s *Scheduler) Acquire(ctx context.Context) (*Slot, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	// Fast path: only grant immediately when nobody is queued, so that
	// queued waiters keep their FIFO priority.
	if len(s.queue) == 0 && s.running < s.limit {
		s.running++
		s.mu.Unlock()
		return &Slot{sched: s}, nil
	}
	req := &request{ch: make(chan struct{})}
	s.queue = append(s.queue, req)
	s.mu.Unlock()

	select {
	case <-req.ch:
		if req.err != nil {
			return nil, req.err
		}
		return &Slot{sched: s}, nil
	case <-ctx.Done():
		s.mu.Lock()
		if req.granted {
			// The grant raced with the cancellation. Hand the slot back
			// so a canceled placeholder never leaks concurrency.
			s.running--
			s.grantLocked()
			s.mu.Unlock()
			return nil, ctx.Err()
		}
		for i, r := range s.queue {
			if r == req {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Release returns the slot to the scheduler, possibly waking the next waiter.
func (sl *Slot) Release() {
	sl.once.Do(func() { sl.sched.release() })
}

// Run acquires a slot, runs fn, and releases the slot when fn returns.
func (s *Scheduler) Run(ctx context.Context, fn func()) error {
	slot, err := s.Acquire(ctx)
	if err != nil {
		return err
	}
	defer slot.Release()
	fn()
	return nil
}

// SetLimit changes the concurrency limit. Lowering it never interrupts
// running tasks; new grants are blocked until running drops below the new
// limit. Raising it immediately grants queued waiters, in FIFO order.
// A negative limit is treated as zero.
func (s *Scheduler) SetLimit(limit int) {
	if limit < 0 {
		limit = 0
	}
	s.mu.Lock()
	s.limit = limit
	s.grantLocked()
	s.mu.Unlock()
}

// Close shuts the scheduler down. Queued waiters are woken with ErrClosed
// and future Acquire calls fail with ErrClosed. Slots already granted remain
// valid and may still be released. Close is idempotent.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, req := range s.queue {
		req.err = ErrClosed
		close(req.ch)
	}
	s.queue = nil
	s.mu.Unlock()
}

// Limit returns the current concurrency limit.
func (s *Scheduler) Limit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

// Running returns the number of slots currently granted.
func (s *Scheduler) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Queued returns the number of waiters currently queued.
func (s *Scheduler) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// grantLocked grants slots to queued waiters, in FIFO order, while
// running < limit. Callers must hold s.mu.
func (s *Scheduler) grantLocked() {
	for len(s.queue) > 0 && s.running < s.limit {
		req := s.queue[0]
		s.queue = s.queue[1:]
		req.granted = true
		s.running++
		close(req.ch)
	}
}

func (s *Scheduler) release() {
	s.mu.Lock()
	s.running--
	s.grantLocked()
	s.mu.Unlock()
}
