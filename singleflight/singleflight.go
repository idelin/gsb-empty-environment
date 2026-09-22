// Package singleflight ensures that concurrent calls for the same key
// execute fn at most once and share its result.
//
// Every caller may cancel independently. When all callers for a key cancel,
// the context passed to fn is cancelled so the computation can terminate.
// Completed, failed, or panicked calls are never cached: a new call arriving
// after the in-flight call finishes starts a fresh computation.
package singleflight

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// PanicError is returned to every waiter when the shared function panics.
// It wraps the recovered value and the captured stack trace.
type PanicError struct {
	Value interface{}
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("singleflight: fn panicked: %v", e.Value)
}

// Group deduplicates concurrent calls by key.
// A Group is safe for concurrent use and may be reused for the lifetime
// of the process. The zero value is ready to use.
type Group struct {
	mu sync.Mutex
	m  map[string]*call

	// afterAttach is notified (under mu) whenever a caller attaches to a
	// freshly created or in-flight call. It is nil outside of tests and is
	// used to make scheduling deterministic.
	afterAttach func(key string, inFlight bool)
	// afterCloseLocked runs immediately after a call is marked closed and
	// removed from the map, but before its result is delivered to waiters.
	// Tests block here while a new caller enters, pinning the
	// completion-vs-new-call race window open deterministically.
	afterCloseLocked func(key string)
}

type call struct {
	// ctx is cancelled when every waiter has left. fn receives ctx so it
	// can stop its work promptly once nobody is interested anymore.
	ctx    context.Context
	cancel context.CancelCauseFunc

	// closed means the call is terminal: either fn has finished or every
	// waiter cancelled before it finished. Closed calls never accept new
	// waiters; the leader deletes the map entry while setting it.
	closed bool

	waiters []*waiter
}

type waiter struct {
	// ch is buffered with capacity 1; it receives the shared outcome
	// exactly once and never blocks the leader.
	ch  chan result
	key string
}

type result struct {
	val interface{}
	err error
}

// Do executes fn once per concurrently-active key and returns its result to
// every caller sharing the execution.
//
// If a call for key is in flight, Do waits for it and returns the shared
// result, unless ctx is cancelled first. If this caller is the last waiter
// and its ctx is cancelled, the context handed to fn is cancelled as well.
// A panic inside fn is converted to *PanicError for all waiters. Results are
// never memoized: the next Do after completion runs fn again.
func (g *Group) Do(ctx context.Context, key string, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}

	w := &waiter{ch: make(chan result, 1), key: key}
	if c, ok := g.m[key]; ok {
		c.waiters = append(c.waiters, w)
		if g.afterAttach != nil {
			g.afterAttach(key, true)
		}
		g.mu.Unlock()
		return g.wait(ctx, c, w)
	}

	callCtx, cancel := context.WithCancelCause(context.Background())
	c := &call{ctx: callCtx, cancel: cancel, waiters: []*waiter{w}}
	g.m[key] = c
	if g.afterAttach != nil {
		g.afterAttach(key, false)
	}
	g.mu.Unlock()

	go g.run(key, c, fn)
	return g.wait(ctx, c, w)
}

// wait blocks for either the shared outcome or the caller's own cancellation.
// Both channels may be ready simultaneously; a result that was already
// delivered before cancellation always wins, so waiters never miss the value
// they waited for.
func (g *Group) wait(ctx context.Context, c *call, w *waiter) (interface{}, error) {
	select {
	case r := <-w.ch:
		return r.val, r.err
	case <-ctx.Done():
		g.removeWaiter(c, w)
		// Drain a result that raced with our removal. The send into the
		// capacity-1 buffered channel cannot block; the value may simply
		// have not been selected yet.
		select {
		case r := <-w.ch:
			return r.val, r.err
		default:
			return nil, ctx.Err()
		}
	}
}

// removeWaiter detaches one caller. When it was the last waiter, it closes the
// call (if still open), cancels fn's context, and removes the map entry only
// if the map still points at this exact call, so a replacement call created by
// another goroutine is never touched.
func (g *Group) removeWaiter(c *call, w *waiter) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, cand := range c.waiters {
		if cand == w {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			break
		}
	}
	if len(c.waiters) != 0 || c.closed {
		return
	}

	// This was the last waiter. Mark the call terminal, ask fn to stop, and
	// remove the map entry only if it still identifies THIS call: a new call
	// for the same key created concurrently must survive untouched.
	c.closed = true
	c.cancel(context.Canceled)
	if g.m[w.key] == c {
		delete(g.m, w.key)
	}
}

// run executes fn on behalf of the whole group of waiters. It converts a
// panic into an explicit error, publishes the outcome to every waiter, and
// removes the call from the map while holding mu and before any delivery so a
// concurrent Do cannot reuse a failed or completed call.
func (g *Group) run(key string, c *call, fn func(ctx context.Context) (interface{}, error)) {
	var res result
	func() {
		defer func() {
			if r := recover(); r != nil {
				res.err = &PanicError{Value: r, Stack: debug.Stack()}
			}
		}()
		res.val, res.err = fn(c.ctx)
	}()

	g.mu.Lock()
	if c.closed {
		// All waiters cancelled before fn finished; nobody to deliver to.
		// The map entry was already removed by the last waiter.
		g.mu.Unlock()
		return
	}
	c.closed = true
	waiters := c.waiters
	delete(g.m, key)
	hook := g.afterCloseLocked
	g.mu.Unlock()

	// The map entry is already gone, but the shared outcome has not been
	// handed out yet. A Do arriving in this window starts a fresh call.
	// (The test hook blocks here to pin that window open.)
	if hook != nil {
		hook(key)
	}
	for _, w := range waiters {
		w.ch <- res
	}
}
