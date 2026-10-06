// Package taskgroup runs a tree of cooperating tasks and reports the
// earliest-created business failure.
//
// A Group runs tasks that may themselves launch child tasks, forming a
// tree. Wait blocks until every task in the tree, including all
// descendants, has returned. The first non-cancellation error cancels
// the whole tree; once every task has exited, Wait reports the
// non-cancellation error produced by the task with the smallest
// creation sequence number. Cancellation errors never replace a
// business error. Launching new tasks after Wait has returned is a
// misuse and panics.
package taskgroup

import (
	"context"
	"sync"
)

// Task is a unit of work in the task tree. It may launch child tasks
// through children to grow the tree. The group finishes only after
// every descendant has returned.
type Task func(ctx context.Context, children *Group) error

// Group tracks a tree of running tasks. Create one with WithContext.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	mu        sync.Mutex
	nextSeq   int64
	done      bool
	err       error
	errSeq    int64
	cancelErr error
	cancelSeq int64
}

// WithContext returns a new Group and a context that is canceled when
// the first task fails with a non-cancellation error or when Wait
// returns, whichever happens first.
func WithContext(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	return &Group{ctx: ctx, cancel: cancel}, ctx
}

// Go launches t as a new task in the tree. It panics if the group has
// already finished, i.e. Wait has returned.
func (g *Group) Go(t Task) {
	g.mu.Lock()
	if g.done {
		g.mu.Unlock()
		panic("taskgroup: Go called after Wait returned")
	}
	seq := g.nextSeq
	g.nextSeq++
	g.wg.Add(1)
	g.mu.Unlock()

	go func() {
		defer g.wg.Done()
		g.record(seq, t(g.ctx, g))
	}()
}

// Wait blocks until every task in the tree, including all descendants,
// has returned. It reports the non-cancellation error from the task
// with the smallest creation sequence number. Cancellation errors are
// reported only when no business error occurred.
func (g *Group) Wait() error {
	g.wg.Wait()
	g.cancel()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.done = true
	if g.err != nil {
		return g.err
	}
	return g.cancelErr
}

// record stores err, keeping the non-cancellation error with the
// smallest creation sequence number. The first business failure
// cancels the whole tree.
func (g *Group) record(seq int64, err error) {
	if err == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if isCancel(err) {
		if g.cancelErr == nil || seq < g.cancelSeq {
			g.cancelErr, g.cancelSeq = err, seq
		}
		return
	}
	if g.err == nil || seq < g.errSeq {
		g.err, g.errSeq = err, seq
	}
	g.cancel()
}

// isCancel reports whether err is or wraps a cancellation error.
func isCancel(err error) bool {
	for err != nil {
		if err == context.Canceled || err == context.DeadlineExceeded {
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
