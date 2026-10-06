// Package taskgroup runs a tree of related tasks spawned by a single
// computation, with shared cancellation and deterministic error selection.
package taskgroup

import (
	"context"
	"errors"
	"sync"
)

// reportHook, when non-nil, is invoked after a task reports its result.
// It exists for tests that need to observe result arrival order.
var reportHook func(seq uint64, err error)

type taskFunc func(ctx context.Context, t *Tasks) error

// A Group runs a tree of tasks derived from one computation.
//
// The first non-cancellation error reported by any task cancels the group
// context, propagating cancellation to the whole tree. Wait reports the
// non-cancellation error created earliest (smallest task creation
// sequence); cancellation errors never replace a business error.
//
// A Group must not be copied after first use.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	seq    uint64 // next task creation sequence
	err    error  // non-cancellation error with the smallest creation sequence
	errSeq uint64

	root node
}

// New returns a Group deriving its context from parent. The context is
// canceled when the first non-cancellation error is reported or when
// parent is canceled.
func New(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	g := &Group{ctx: ctx, cancel: cancel}
	g.root.g = g
	return g, ctx
}

// Go starts fn as a top-level task. It panics if called after Wait.
func (g *Group) Go(fn func(ctx context.Context, t *Tasks) error) {
	g.root.spawn(fn, "taskgroup: Go called after Wait")
}

// Wait blocks until every task in the tree has exited, then reports the
// outcome: the non-cancellation error with the smallest creation
// sequence. If no task reported a business error but the context was
// canceled (e.g. from the parent), Wait returns the context error;
// otherwise it returns nil.
func (g *Group) Wait() error {
	g.root.mu.Lock()
	g.root.closed = true
	g.root.mu.Unlock()

	g.root.wg.Wait()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return g.err
	}
	return g.ctx.Err()
}

// Tasks spawns subtasks of a running task. The owning task is not
// complete until all of its subtasks, recursively, have exited.
type Tasks struct {
	n *node
}

// Go starts fn as a subtask of the owning task. It panics if the owning
// task has already returned.
func (t *Tasks) Go(fn func(ctx context.Context, t *Tasks) error) {
	t.n.spawn(fn, "taskgroup: Go called after the owning task returned")
}

// node tracks the subtasks spawned by one task so the task's completion
// waits for its whole subtree.
type node struct {
	g      *Group
	mu     sync.Mutex
	closed bool // owning function returned; no more subtasks accepted
	wg     sync.WaitGroup
}

func (n *node) spawn(fn taskFunc, closedMsg string) {
	g := n.g

	g.mu.Lock()
	seq := g.seq
	g.seq++
	g.mu.Unlock()

	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		panic(closedMsg)
	}
	n.wg.Add(1)
	n.mu.Unlock()

	child := &node{g: g}
	go func() {
		defer n.wg.Done()
		err := fn(g.ctx, &Tasks{n: child})
		child.mu.Lock()
		child.closed = true
		child.mu.Unlock()
		g.report(seq, err)
		child.wg.Wait()
	}()
}

// report records a task result. Cancellation errors are never recorded,
// so they cannot replace a business error. The first recorded business
// error cancels the group context.
func (g *Group) report(seq uint64, err error) {
	if err != nil && !isCancellation(err) {
		g.mu.Lock()
		if g.err == nil || seq < g.errSeq {
			g.err, g.errSeq = err, seq
		}
		g.mu.Unlock()
		g.cancel()
	}
	if reportHook != nil {
		reportHook(seq, err)
	}
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
