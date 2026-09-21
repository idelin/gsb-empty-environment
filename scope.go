// Package scope implements structured concurrency: child tasks belong to a
// parent scope, the first failure cancels all siblings, and Wait blocks
// until every child (including its cleanup) has finished.
//
// Only the Go standard library is used. Task functions, their timing and
// their failures are fully injected by the caller, which makes the scope
// deterministic to test.
package scope

import (
	"context"
	"errors"
	"sync"
)

// errDone is the cancellation cause used when a scope finishes normally.
var errDone = errors.New("scope: all tasks finished")

// Scope owns a set of child goroutines derived from a parent context.
// The zero value is not usable; create one with New.
type Scope struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	wg sync.WaitGroup

	mu    sync.Mutex
	first error   // first real failure; cancellation may never overwrite it
	errs  []error // every distinct failure, in arrival order
}

// New returns a Scope whose lifetime is bounded by parent.
func New(parent context.Context) *Scope {
	ctx, cancel := context.WithCancelCause(parent)
	return &Scope{ctx: ctx, cancel: cancel}
}

// Context returns the scope's context. It is cancelled when the first
// child fails, when the parent context is cancelled, or when Wait returns.
func (s *Scope) Context() context.Context { return s.ctx }

// Go runs fn as a child of the scope. fn receives the scope context and
// must return when that context is cancelled. A non-nil error returned by
// fn cancels all siblings and is recorded in the aggregated result.
func (s *Scope) Go(fn func(ctx context.Context) error) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := fn(s.ctx); err != nil {
			s.fail(err)
		}
	}()
}

// fail records a child error. Cancellation echoes (children reporting
// ctx.Err() after the scope was cancelled) are translated back to the
// original cause and never recorded twice, so the first real failure
// always stays first.
func (s *Scope) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if errors.Is(err, context.Canceled) {
		if cause := context.Cause(s.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
			err = cause
		}
		if s.first != nil && errors.Is(err, s.first) {
			return // cancellation echo of an already-recorded failure
		}
	}

	if s.first == nil {
		s.first = err
		s.cancel(err) // cancel all siblings, carrying the original cause
	}
	s.errs = append(s.errs, err)
}

// Wait blocks until every child goroutine — including any cleanup it
// performs after cancellation — has returned. It never returns early:
// a failing child and a sibling still cleaning up both must finish first.
//
// The result is nil if all children succeeded, otherwise the failures
// joined in arrival order with the first failure at the head. Calling
// Wait more than once is safe, as is cancellation racing with failures.
func (s *Scope) Wait() error {
	s.wg.Wait()
	s.cancel(errDone) // release resources; repeated cancel is a no-op

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first == nil {
		return nil
	}
	return errors.Join(s.errs...)
}
