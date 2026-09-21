// Package scope provides structured concurrency for Go using only the
// standard library's sync primitives and context.
//
// A Scope owns a group of child tasks. Children belong to the scope:
// the first child failure cancels the scope's context (and therefore
// its siblings), and Wait blocks until every child — including children
// that are still cleaning up after cancellation — has returned.
// Scopes nest: a child may create a child Scope derived from the
// parent's context, and the parent does not return before the whole
// subtree has finished.
package scope

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Scope is a structured-concurrency scope. The zero value is not
// usable; create one with New.
type Scope struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	mu     sync.Mutex
	first  error   // first failure reported; never overwritten
	errs   []error // every failure reported, in arrival order
	waited bool
}

// New returns a Scope whose lifetime is bounded by parent. Cancelling
// parent cancels every task started on the scope.
func New(parent context.Context) *Scope {
	ctx, cancel := context.WithCancel(parent)
	return &Scope{ctx: ctx, cancel: cancel}
}

// Context returns the scope's context. It is cancelled when the first
// child fails, when the parent context is cancelled, or after Wait
// returns.
func (s *Scope) Context() context.Context { return s.ctx }

// Go runs fn as a child task of the scope. fn receives the scope's
// context and must return when that context is cancelled. A non-nil
// return value is recorded as a failure and cancels the scope.
//
// Go panics if called after Wait has returned, because a task started
// then could never be awaited.
func (s *Scope) Go(fn func(ctx context.Context) error) {
	s.mu.Lock()
	if s.waited {
		s.mu.Unlock()
		panic("scope: Go called after Wait")
	}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		if err := fn(s.ctx); err != nil {
			s.fail(err)
		}
	}()
}

// fail records err and cancels the scope. The first failure wins the
// "first" slot; cancellation errors reported by siblings afterwards
// can never overwrite it. cancel is idempotent, so concurrent or
// repeated failures are safe.
func (s *Scope) fail(err error) {
	s.mu.Lock()
	if s.first == nil {
		s.first = err
	}
	s.errs = append(s.errs, err)
	s.mu.Unlock()
	s.cancel()
}

// Wait blocks until every child task has returned — including children
// still running their cleanup after cancellation — and then releases
// the scope's resources. It returns nil if no child failed, otherwise
// an *Error whose First is the original cause and whose All preserves
// every reported failure.
func (s *Scope) Wait() error {
	s.wg.Wait()

	s.mu.Lock()
	s.waited = true
	first := s.first
	errs := append([]error(nil), s.errs...)
	s.mu.Unlock()

	s.cancel() // release context resources; safe to call repeatedly

	if first == nil {
		return nil
	}
	return &Error{First: first, All: errs}
}

// Error is the aggregate failure of a scope. First is the original cause
// (the failure that triggered cancellation); All contains every error
// reported by children, in arrival order, so nothing is lost.
type Error struct {
	First error
	All   []error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope: %v", e.First)
	if extra := len(e.All) - 1; extra > 0 {
		fmt.Fprintf(&b, " (and %d more error(s))", extra)
	}
	return b.String()
}

// Unwrap returns every reported failure, so errors.Is and errors.As
// match the original cause as well as any sibling error (including
// context.Canceled reported by cancelled tasks).
func (e *Error) Unwrap() []error { return e.All }
