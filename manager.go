// Package keyinit provides per-key lazy initialization with result sharing.
//
// A Manager initializes each key on first use. Concurrent callers asking for
// the same key share a single in-flight "round" of initialization and all
// receive the same result. If initialization fails, the error is broadcast to
// every waiter of that round and the next call starts a fresh round, so
// failures are retryable. A round can be cancelled explicitly; a late
// completion from a cancelled (stale) round is discarded by generation
// checking and can never pollute a newer round. Successful results are cached
// until Invalidate is called.
//
// The InitFunc receives a context that carries the chain of keys currently
// being initialized on this call path. An InitFunc may trigger initialization
// of other keys by calling Get with that context; calling Get for a key that
// is already on the chain (direct self-recursion or a cross-key cycle) is
// detected and fails with *RecursionError instead of deadlocking.
package keyinit

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrCancelled is delivered to the waiters of a round aborted by Cancel.
var ErrCancelled = errors.New("keyinit: initialization cancelled")

// RecursionError reports an initialization cycle: the key was requested again
// while it (or a key depending on it) was already being initialized on the
// same call chain.
type RecursionError struct {
	Key   string
	Chain []string // keys currently being initialized, outermost first
}

func (e *RecursionError) Error() string {
	return fmt.Sprintf("keyinit: recursive initialization of key %q (chain %v)", e.Key, e.Chain)
}

// InitFunc produces the value for a key. The provided context carries the
// initialization chain and must be forwarded to any nested Get calls so that
// cycles can be detected. It is cancelled if the round is aborted via Cancel,
// but not when an individual caller's context is cancelled.
type InitFunc func(ctx context.Context) (any, error)

// ctxChainKey is the context key for the initialization chain.
type ctxChainKey struct{}

func chainFrom(ctx context.Context) []string {
	chain, _ := ctx.Value(ctxChainKey{}).([]string)
	return chain
}

func chainHas(ctx context.Context, key string) bool {
	for _, k := range chainFrom(ctx) {
		if k == key {
			return true
		}
	}
	return false
}

func chainWith(ctx context.Context, key string) context.Context {
	prev := chainFrom(ctx)
	chain := make([]string, len(prev)+1)
	copy(chain, prev)
	chain[len(prev)] = key
	return context.WithValue(ctx, ctxChainKey{}, chain)
}

// round is one in-flight initialization attempt for a key. Its result fields
// are written before done is closed and read after, which is safe because
// closing a channel happens-before any receive observing it.
type round struct {
	done   chan struct{}
	cancel context.CancelFunc
	val    any
	err    error
}

// slot holds the per-key state: an optional in-flight round and the cached
// result of the last successful round.
type slot struct {
	round *round
	val   any
	ok    bool
}

// Manager coordinates per-key initialization. The zero value is ready to use.
type Manager struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{slots: make(map[string]*slot)}
}

// Get returns the value for key, initializing it with fn on first use.
//
// Concurrent calls for the same key share one round: fn runs at most once per
// round and every caller of that round observes the same value or error. A
// failed round is not cached; the next Get starts a new round. A successful
// result is cached until Invalidate is called.
//
// If ctx is cancelled while waiting, Get returns ctx.Err() but the round
// itself continues for the remaining waiters.
func (m *Manager) Get(ctx context.Context, key string, fn InitFunc) (any, error) {
	if chainHas(ctx, key) {
		return nil, &RecursionError{Key: key, Chain: append(chainFrom(ctx), key)}
	}

	m.mu.Lock()
	if m.slots == nil {
		m.slots = make(map[string]*slot)
	}
	s := m.slots[key]
	if s == nil {
		s = &slot{}
		m.slots[key] = s
	}
	if s.ok {
		val := s.val
		m.mu.Unlock()
		return val, nil
	}
	if s.round != nil {
		r := s.round
		m.mu.Unlock()
		return waitRound(ctx, r)
	}

	r := &round{done: make(chan struct{})}
	s.round = r
	m.mu.Unlock()

	// Detach the caller's cancellation (values, including the chain, are
	// kept) so one caller giving up does not abort the shared round.
	fnCtx, cancel := context.WithCancel(chainWith(context.WithoutCancel(ctx), key))
	r.cancel = cancel
	go func() {
		val, err := fn(fnCtx)
		m.finish(key, s, r, val, err)
	}()

	return waitRound(ctx, r)
}

func waitRound(ctx context.Context, r *round) (any, error) {
	select {
	case <-r.done:
		return r.val, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// finish publishes the outcome of r. If r is no longer the current round for
// the key (it was cancelled or otherwise superseded), the outcome is stale
// and discarded so a late callback cannot pollute the new round.
func (m *Manager) finish(key string, s *slot, r *round, val any, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.round != r {
		return
	}
	s.round = nil
	if err == nil {
		s.val, s.ok = val, true
	} else if !s.ok {
		// Nothing cached and nothing in flight: drop the empty slot.
		delete(m.slots, key)
	}
	r.val, r.err = val, err
	close(r.done)
}

// Cancel aborts the in-flight round for key, if any. Waiters of that round
// receive ErrCancelled and the round's context is cancelled. The next Get
// starts a fresh round; if the aborted InitFunc completes later, its result
// is discarded. Reports whether a round was cancelled.
func (m *Manager) Cancel(key string) bool {
	m.mu.Lock()
	s := m.slots[key]
	if s == nil || s.round == nil {
		m.mu.Unlock()
		return false
	}
	r := s.round
	s.round = nil
	if !s.ok {
		delete(m.slots, key)
	}
	r.val, r.err = nil, ErrCancelled
	close(r.done)
	m.mu.Unlock()

	r.cancel()
	return true
}

// Invalidate drops the cached value for key so the next Get re-initializes
// it. It does not affect a round currently in flight; use Cancel for that.
func (m *Manager) Invalidate(key string) {
	m.mu.Lock()
	if s := m.slots[key]; s != nil {
		s.val, s.ok = nil, false
		if s.round == nil {
			delete(m.slots, key)
		}
	}
	m.mu.Unlock()
}
