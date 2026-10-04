// Package taskexec implements a recursive task executor with a bounded
// number of execution slots. A task that spawns children and waits for
// them temporarily lends out its slot so descendants can run; it must
// re-acquire a slot before resuming. Because every waiting task releases
// its slot before blocking, nested waits cannot deadlock: there is always
// a free slot for the deepest runnable descendant.
package taskexec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// Task is a unit of work. While its function runs it holds one executor
// slot. It may spawn children via the Worker and wait for them.
type Task func(ctx context.Context, w *Worker) error

// Executor bounds the number of tasks running concurrently.
type Executor struct {
	slots    chan struct{}
	inflight atomic.Int64
	peak     atomic.Int64
}

// New returns an Executor allowing at most maxParallel tasks to hold a
// slot at the same time.
func New(maxParallel int) *Executor {
	if maxParallel < 1 {
		panic("taskexec: maxParallel must be >= 1")
	}
	return &Executor{slots: make(chan struct{}, maxParallel)}
}

// InFlight reports how many slots are currently held.
func (ex *Executor) InFlight() int64 { return ex.inflight.Load() }

// Peak reports the maximum number of slots held simultaneously so far.
func (ex *Executor) Peak() int64 { return ex.peak.Load() }

func (ex *Executor) acquire(ctx context.Context) error {
	select {
	case ex.slots <- struct{}{}:
		n := ex.inflight.Add(1)
		for {
			p := ex.peak.Load()
			if n <= p || ex.peak.CompareAndSwap(p, n) {
				break
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ex *Executor) release() {
	<-ex.slots
	ex.inflight.Add(-1)
}

// Run executes the root task and blocks until it and all of its
// descendants finish. It returns the root's error joined with any
// descendant errors.
func (ex *Executor) Run(ctx context.Context, root Task) error {
	if err := ex.acquire(ctx); err != nil {
		return err
	}
	w := newWorker(ex, ctx)
	w.slotHeld = true
	err := root(w.ctx, w)
	waitErr := w.waitAndExit()
	w.cancel()
	return errors.Join(err, waitErr)
}

// Worker is the handle a running task uses to spawn and await children.
// It is only safe to call Spawn and Wait from the task's own goroutine.
type Worker struct {
	ex     *Executor
	ctx    context.Context
	cancel context.CancelFunc

	wg   sync.WaitGroup
	mu   sync.Mutex
	errs []error

	slotHeld bool
}

func newWorker(ex *Executor, ctx context.Context) *Worker {
	cctx, cancel := context.WithCancel(ctx)
	return &Worker{ex: ex, ctx: cctx, cancel: cancel}
}

// Context returns the context governing this worker's subtree.
func (w *Worker) Context() context.Context { return w.ctx }

// Spawn starts t as a child task. The child acquires its own slot before
// running, so it may block until a slot is lent out by a waiting ancestor
// or freed by a finishing task.
func (w *Worker) Spawn(t Task) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		if err := w.ex.acquire(w.ctx); err != nil {
			w.recordErr(err)
			return
		}
		child := newWorker(w.ex, w.ctx)
		child.slotHeld = true
		err := t(child.ctx, child)
		waitErr := child.waitAndExit()
		child.cancel()
		w.recordErr(errors.Join(err, waitErr))
	}()
}

// Wait lends out the caller's slot, blocks until all children spawned so
// far have finished, then re-acquires a slot before returning. It returns
// the joined errors of the children observed since the previous Wait.
//
// If the worker's context is cancelled while waiting to re-acquire, Wait
// returns without a slot; the task should then return promptly.
func (w *Worker) Wait() error {
	w.mu.Lock()
	if w.slotHeld {
		w.slotHeld = false
		w.mu.Unlock()
		w.ex.release()
	} else {
		w.mu.Unlock()
	}

	w.wg.Wait()

	if err := w.ex.acquire(w.ctx); err != nil {
		w.mu.Lock()
		errs := errors.Join(w.errs...)
		w.errs = nil
		w.mu.Unlock()
		return errors.Join(errs, err)
	}

	w.mu.Lock()
	w.slotHeld = true
	errs := errors.Join(w.errs...)
	w.errs = nil
	w.mu.Unlock()
	return errs
}

// waitAndExit releases the slot (if held) and waits for children without
// re-acquiring, because the owning task is finished.
func (w *Worker) waitAndExit() error {
	w.mu.Lock()
	held := w.slotHeld
	w.slotHeld = false
	w.mu.Unlock()
	if held {
		w.ex.release()
	}
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.errs...)
}

func (w *Worker) recordErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.errs = append(w.errs, err)
	w.mu.Unlock()
}
