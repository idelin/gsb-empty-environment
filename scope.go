// Package scopex implements structured-concurrency scopes using only the Go
// standard library.
//
// A Scope owns a group of subtasks. Subtasks belong to the scope that
// spawned them: if any subtask fails, the scope's context is canceled so
// its siblings observe the failure and can clean up. Wait blocks until
// every subtask has fully returned, so a scope never exits while a child
// is still cleaning up. The first failure is preserved as the cause of
// the scope; cancellation of siblings never overrides it, and additional
// failures are aggregated with errors.Join.
package scopex

import (
	"context"
	"errors"
	"sync"
)

// Scope is a structured-concurrency scope. The zero value is not usable;
// create one with New or Run.
type Scope struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	wg sync.WaitGroup

	mu    sync.Mutex
	first error   // first failure; the cause of the scope
	rest  []error // subsequent non-cancellation failures
}

// New returns a Scope whose lifetime is bounded by parent. When the parent
// context is canceled, all subtasks of the scope observe the cancellation.
func New(parent context.Context) *Scope {
	ctx, cancel := context.WithCancelCause(parent)
	return &Scope{ctx: ctx, cancel: cancel}
}

// Run creates a Scope derived from ctx, invokes fn with it, and then waits
// for every subtask spawned inside fn to finish. The returned error is the
// scope's aggregated failure (including fn's own error, if any).
func Run(ctx context.Context, fn func(s *Scope) error) error {
	s := New(ctx)
	if fn != nil {
		s.record(fn(s))
	}
	return s.Wait()
}

// Context returns the scope's context. It is canceled when any subtask
// fails, when Cancel is called, or when the parent context is canceled.
func (s *Scope) Context() context.Context { return s.ctx }

// Cancel cancels the scope's context, signaling all subtasks to stop.
// It is idempotent: repeated calls are safe and never change the cause
// established by the first cancellation or failure.
func (s *Scope) Cancel() { s.cancel(context.Canceled) }

// Go spawns fn as a subtask of the scope. The subtask receives the scope's
// context and must return when it is canceled. A non-nil return value is
// recorded as a failure and triggers cancellation of the whole scope.
func (s *Scope) Go(fn func(ctx context.Context) error) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.record(fn(s.ctx))
	}()
}

// Scope runs fn as a nested scope inside a subtask of s. The nested scope
// derives from s's context, so a failure in the parent cancels the nested
// subtasks, and a failure inside the nested scope propagates upward. The
// nested scope's own Wait completes before its subtask returns, so no
// goroutine can leak past the parent's Wait.
func (s *Scope) Scope(fn func(child *Scope) error) {
	s.Go(func(ctx context.Context) error {
		child := New(ctx)
		if fn != nil {
			child.record(fn(child))
		}
		return child.Wait()
	})
}

// record stores err according to the scope's error policy:
//
//   - nil errors are ignored;
//   - the first failure becomes the cause of the scope and cancels it;
//   - cancellation errors observed after a failure already exists are
//     dropped, so sibling cleanup can never override the first failure;
//   - copies of the established cause propagated back through a nested
//     scope's context are dropped, so a failure is never double-counted;
//   - any other later failure is kept for aggregation.
func (s *Scope) record(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first != nil && (errors.Is(err, context.Canceled) || errors.Is(err, s.first)) {
		return
	}
	if s.first == nil {
		s.first = err
		s.cancel(err)
		return
	}
	s.rest = append(s.rest, err)
}

// Wait blocks until every subtask of the scope has returned, including
// subtasks that are still cleaning up after a sibling's failure. It then
// releases the scope's context and returns the aggregated error: nil if
// no subtask failed and the scope was never canceled, the first failure
// if exactly one occurred, or an errors.Join of all failures (first
// failure leading) otherwise. If the scope was canceled without any
// subtask failure (via Cancel or the parent context), Wait reports that
// cancellation cause. The result unwraps to every recorded cause.
func (s *Scope) Wait() error {
	s.wg.Wait()
	cause := context.Cause(s.ctx)
	s.cancel(context.Canceled) // release resources; cannot override first cause

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.first == nil && cause != nil:
		return cause
	case s.first == nil:
		return nil
	case len(s.rest) == 0:
		return s.first
	default:
		return errors.Join(append([]error{s.first}, s.rest...)...)
	}
}
