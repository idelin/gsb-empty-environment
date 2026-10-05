// Package migflight is a single-flight request group whose in-flight calls
// can migrate from a temporary key to a formal key.
//
// A call started under a temporary key may, while still running, promote
// itself to a formal key via Handle.Promote:
//
//   - If the formal key has no in-flight call, the running call is re-registered
//     under the formal key and keeps executing; future requests for the formal
//     key join it.
//   - If the formal key already has an in-flight call, the temporary call's
//     waiters are merged into the existing call and receive its result.
//
// Migration detaches the old key, so new requests for the old key start a
// fresh, explicitly numbered generation instead of joining the migrated call.
//
// The protocol is deadlock-free: all shared state is guarded by a single
// mutex, user functions run outside the lock, and waiters block on channels,
// never on the mutex. Cancelling one waiter's context never affects others.
package migflight

import (
	"context"
	"sync"
)

// call is one in-flight (or merged) execution.
type call struct {
	done chan struct{} // closed when the result is ready or the call was merged
	val  any
	err  error
	dups int // number of waiters that joined this call

	// mergedInto, when non-nil at the time done is closed, redirects waiters
	// to the call that now owns the result.
	mergedInto *call
	closed     bool
}

// Result is the outcome of a Do/DoCtx call.
type Result struct {
	Val    any
	Err    error
	Shared bool   // true if the value was produced by another goroutine's call
	Gen    uint64 // generation of the key this call waited on
}

// Group is a single-flight request group. The zero value is ready to use.
type Group struct {
	mu    sync.Mutex
	calls map[string]*call
	gens  map[string]uint64
}

// NewGroup returns an initialized Group.
func NewGroup() *Group {
	return &Group{calls: make(map[string]*call), gens: make(map[string]uint64)}
}

// Handle is passed to the executing function and lets it promote its
// temporary key to a formal key.
type Handle struct {
	g   *Group
	key string
	gen uint64
	c   *call

	formalKey string // key the call is currently registered under after Promote
	finished  bool
}

// Key returns the temporary key the call was started with.
func (h *Handle) Key() string { return h.key }

// Gen returns the generation of the temporary key this call belongs to.
func (h *Handle) Gen() uint64 { return h.gen }

// Promote migrates the in-flight call from its current key to formalKey.
//
// It reports whether the call was merged into an existing call already
// registered under formalKey. On merge, this call's waiters receive the
// existing call's result; this call's own eventual result is discarded for
// waiters (the executing function still gets its own return value from Do).
//
// Promote must be called at most once per Handle and only while the function
// is still running; otherwise it is a no-op reporting false.
func (h *Handle) Promote(formalKey string) (merged bool) {
	g := h.g
	g.mu.Lock()
	defer g.mu.Unlock()

	if h.finished || formalKey == h.currentKeyLocked() {
		return false
	}

	// Detach from every key currently pointing at this call, so new requests
	// for those keys start a fresh generation.
	g.detachLocked(h.key, h.c)
	g.detachLocked(h.formalKey, h.c)
	h.formalKey = ""

	if existing, ok := g.calls[formalKey]; ok && existing != h.c {
		// Formal key already has an in-flight call: merge waiters into it.
		h.c.mergedInto = existing
		h.c.closed = true
		close(h.c.done)
		return true
	}

	g.calls[formalKey] = h.c
	h.formalKey = formalKey
	return false
}

func (h *Handle) currentKeyLocked() string {
	if h.formalKey != "" {
		return h.formalKey
	}
	return h.key
}

// finish publishes the result and releases the key registrations.
func (h *Handle) finish(val any, err error) {
	g := h.g
	g.mu.Lock()
	defer g.mu.Unlock()
	if h.finished {
		return
	}
	h.finished = true
	g.detachLocked(h.key, h.c)
	g.detachLocked(h.formalKey, h.c)
	if h.c.closed {
		return // merged: waiters follow mergedInto; this result is discarded
	}
	h.c.val = val
	h.c.err = err
	h.c.closed = true
	close(h.c.done)
}

// detachLocked removes key from the registry if it still points at c and
// advances the key's generation, so the next request starts a new one.
func (g *Group) detachLocked(key string, c *call) {
	if key == "" {
		return
	}
	if g.calls[key] == c {
		delete(g.calls, key)
		g.gens[key]++
	}
}

// Do executes fn for key, deduplicating concurrent calls: while one call for
// key is in flight, other callers wait and share its result.
func (g *Group) Do(key string, fn func(h *Handle) (any, error)) Result {
	return g.DoCtx(context.Background(), key, fn)
}

// DoCtx is Do with a cancellable wait. If ctx is cancelled while waiting,
// DoCtx returns the context error; the in-flight call and all other waiters
// are unaffected.
func (g *Group) DoCtx(ctx context.Context, key string, fn func(h *Handle) (any, error)) Result {
	g.lazyInit()
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		c.dups++
		gen := g.gens[key]
		g.mu.Unlock()
		r := waitCall(ctx, c)
		r.Gen = gen
		return r
	}
	c := &call{done: make(chan struct{})}
	g.calls[key] = c
	gen := g.gens[key]
	g.mu.Unlock()

	h := &Handle{g: g, key: key, gen: gen, c: c}
	val, err := fn(h)
	h.finish(val, err)
	return Result{Val: val, Err: err, Gen: gen}
}

func (g *Group) lazyInit() {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call)
		g.gens = make(map[string]uint64)
	}
	g.mu.Unlock()
}

// waitCall blocks until c's result is ready, following merge redirects.
// Each waiter selects on its own context, so cancellation is strictly local.
func waitCall(ctx context.Context, c *call) Result {
	for {
		select {
		case <-c.done:
			if next := c.mergedInto; next != nil {
				c = next
				continue
			}
			return Result{Val: c.val, Err: c.err, Shared: true}
		case <-ctx.Done():
			return Result{Err: ctx.Err()}
		}
	}
}
