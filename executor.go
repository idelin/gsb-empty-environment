// Package executor implements a recursive task executor with a bounded
// number of concurrency slots.
//
// A task may spawn child tasks and wait for them. While a task is blocked
// waiting for its children, it lends its concurrency slot back to the pool
// so that descendants can run; before resuming, it re-acquires a slot.
// Because waiting tasks never hold slots, nested waits cannot deadlock as
// long as the task tree is finite: there is always a leaf task able to make
// progress, and finishing a leaf frees the slot its parent needs to resume.
package executor

import (
	"context"
	"sync"
	"sync/atomic"
)

// TaskFunc is a unit of work. It receives a context that is canceled when
// the run is canceled or a sibling task fails, and a Scope used to spawn
// and await child tasks.
type TaskFunc func(ctx context.Context, s *Scope) error

// Executor runs recursive task trees with at most Limit tasks holding a
// concurrency slot (i.e. actively running rather than waiting) at any time.
type Executor struct {
	sem     chan struct{}
	running atomic.Int64
	peak    atomic.Int64
}

// New returns an Executor allowing up to limit concurrently running tasks.
func New(limit int) *Executor {
	if limit <= 0 {
		panic("executor: limit must be positive")
	}
	return &Executor{sem: make(chan struct{}, limit)}
}

// Limit returns the maximum number of concurrently running tasks.
func (e *Executor) Limit() int { return cap(e.sem) }

// Running returns the number of slots currently held by running tasks.
func (e *Executor) Running() int { return int(e.running.Load()) }

// Peak returns the maximum number of slots held simultaneously so far.
func (e *Executor) Peak() int { return int(e.peak.Load()) }

// Run executes fn as the root of a task tree and returns the first error
// produced by fn or any descendant task.
func (e *Executor) Run(ctx context.Context, fn TaskFunc) error {
	return runTask(e, ctx, fn)
}

func (e *Executor) acquire(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		n := e.running.Add(1)
		for {
			p := e.peak.Load()
			if n <= p || e.peak.CompareAndSwap(p, n) {
				break
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Executor) release() {
	e.running.Add(-1)
	<-e.sem
}

// lease tracks ownership of one concurrency slot by a single task
// goroutine, making lend/regain idempotent.
type lease struct {
	ex   *Executor
	held atomic.Bool
}

// lend returns the slot to the pool if currently held.
func (l *lease) lend() {
	if l.held.Swap(false) {
		l.ex.release()
	}
}

// regain blocks until a slot is held again or ctx is done.
func (l *lease) regain(ctx context.Context) error {
	if l.held.Load() {
		return nil
	}
	if err := l.ex.acquire(ctx); err != nil {
		return err
	}
	l.held.Store(true)
	return nil
}

// Scope is the handle a task uses to spawn and await its children.
type Scope struct {
	ex        *Executor
	ctx       context.Context
	cancel    context.CancelFunc
	regainCtx context.Context // not canceled on sibling failure
	lease     *lease

	wg  sync.WaitGroup
	mu  sync.Mutex
	err error
}

// Context returns the scope's context, canceled when the run is canceled
// or any child of this scope fails.
func (s *Scope) Context() context.Context { return s.ctx }

// Spawn starts fn as a child task. The child acquires its own slot and may
// block until one is lent or released.
func (s *Scope) Spawn(fn TaskFunc) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := runTask(s.ex, s.ctx, fn); err != nil {
			s.mu.Lock()
			if s.err == nil {
				s.err = err
				s.cancel()
			}
			s.mu.Unlock()
		}
	}()
}

// Wait blocks until all children spawned so far finish. While waiting, the
// calling task lends its slot to the pool; it re-acquires a slot before
// Wait returns. Wait returns the first child error, if any.
func (s *Scope) Wait() error {
	s.lease.lend()
	s.wg.Wait()
	rerr := s.lease.regain(s.regainCtx)
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return rerr
}

func runTask(ex *Executor, ctx context.Context, fn TaskFunc) error {
	if err := ex.acquire(ctx); err != nil {
		return err
	}
	l := &lease{ex: ex}
	l.held.Store(true)
	defer l.lend()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Scope{ex: ex, ctx: sctx, cancel: cancel, regainCtx: ctx, lease: l}

	err := fn(sctx, s)
	werr := s.Wait()
	if err != nil {
		return err
	}
	return werr
}
