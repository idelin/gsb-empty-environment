// Package taskscope runs a group of subtasks bound to one cancellable scope.
//
// Subtasks inherit the scope's context. The scope is cancelled (and sibling
// subtasks are signalled) when the scope is cancelled explicitly, when its
// parent context is cancelled, or when any subtask returns a
// non-cancellation error. Wait blocks until every subtask has exited,
// including subtasks that are still running cleanup after observing
// cancellation.
package taskscope

import (
	"context"
	"errors"
	"sync"
)

// Scope owns a group of subtasks. A Scope must not be copied after first use.
type Scope struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	wg sync.WaitGroup

	mu     sync.Mutex
	closed bool
	errs   []error
}

// New creates a Scope derived from parent. A nil parent is treated as
// context.Background, so scopes may be nested: a child scope inherits
// cancellation from the parent scope's context.
func New(parent context.Context) *Scope {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &Scope{ctx: ctx, cancel: cancel}
}

// Go runs fn as a subtask of the scope. fn receives the scope context,
// which is cancelled when a sibling fails or the scope is cancelled. A fn
// that observes ctx.Done should finish cleanup and return ctx.Err() (or
// nil); cancellation errors are not collected by Wait.
//
// Go panics if called after Wait has returned.
func (s *Scope) Go(fn func(ctx context.Context) error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		panic("taskscope: Go called after Wait")
	}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := fn(s.ctx); err != nil {
			s.fail(err)
		}
	}()
}

// fail records a non-cancellation error and cancels the scope with that
// error as the cause. Context keeps only the first cancellation cause, and
// the error slice is append-only under the mutex, so cancellation can
// never replace or reorder the first failure.
func (s *Scope) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()
	s.cancel(err)
}

// Cancel cancels the scope. It is safe to call concurrently, repeatedly,
// before any subtask is started, or after Wait has returned.
func (s *Scope) Cancel() {
	s.cancel(context.Canceled)
}

// Wait blocks until every subtask has exited and returns all
// non-cancellation errors joined with errors.Join. It returns nil when all
// subtasks succeeded or were only cancelled. Every original cause is
// preserved, so errors.Is and errors.As on the returned error reach each
// subtask error. Wait is safe to call repeatedly and concurrently.
func (s *Scope) Wait() error {
	s.wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return errors.Join(s.errs...)
}

// Ctx returns the scope's context. Subtasks receive it directly; nested
// scopes should be derived from it so that a parent failure cancels them.
func (s *Scope) Ctx() context.Context { return s.ctx }
