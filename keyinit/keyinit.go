// Package keyinit provides per-key lazy initialization.
//
// A Manager initializes the value for a key on first use. Concurrent calls
// for the same key join the same round and share its result: the init
// function runs at most once per round. A successful result is cached until
// invalidated; a failure is broadcast to all waiters of the round and the
// next call starts a fresh round (retry). A round that completes after it
// has been invalidated is a "late" round: its result is delivered to its own
// waiters but never written back, so it cannot pollute a newer round.
//
// An InitFunc may call Manager.Do for other keys using the context it
// receives. Calling it for a key already being initialized in the same call
// chain (directly or through a cycle) is detected and fails with
// ErrRecursiveInit instead of deadlocking.
package keyinit

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrRecursiveInit is returned by Manager.Do when the key is already being
// initialized in the current call chain (same-key recursion or a cross-key
// cycle such as A -> B -> A).
var ErrRecursiveInit = errors.New("keyinit: recursive initialization detected")

// InitFunc produces the value for a key. The provided context carries the
// chain of keys currently being initialized; pass it (or a child of it) to
// any nested Manager.Do call so recursion can be detected. The context is
// canceled if the caller that started the round is canceled.
type InitFunc func(ctx context.Context) (interface{}, error)

// round is one initialization attempt for a key. done is closed when the
// round completes; val and err are set before done is closed.
type round struct {
	done chan struct{}
	val  interface{}
	err  error
}

// Manager tracks in-flight rounds and cached values per key. Keys must be
// comparable (usable as Go map keys). The zero value is not ready for use;
// use NewManager.
type Manager struct {
	mu     sync.Mutex
	rounds map[interface{}]*round
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{rounds: make(map[interface{}]*round)}
}

// Do returns the value for key, initializing it with fn if needed.
//
// If a round for key is in flight, Do waits for it and shares its result;
// fn is not called. If a previous round succeeded, the cached value is
// returned. If a previous round failed (or was invalidated), Do starts a
// new round and calls fn. The caller that starts the round runs fn
// synchronously; its context is the one passed to fn.
//
// A caller whose own ctx is canceled while waiting stops waiting and
// returns ctx.Err() without affecting the round.
func (m *Manager) Do(ctx context.Context, key interface{}, fn InitFunc) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if inChain(ctx, key) {
		return nil, ErrRecursiveInit
	}

	m.mu.Lock()
	if r, ok := m.rounds[key]; ok {
		m.mu.Unlock()
		select {
		case <-r.done:
			return r.val, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := &round{done: make(chan struct{})}
	m.rounds[key] = r
	m.mu.Unlock()

	return m.run(key, r, ctx, fn)
}

// Invalidate drops any cached value for key and detaches an in-flight
// round: when that round eventually completes, its result is still
// delivered to its own waiters but is not stored, so it cannot overwrite a
// newer round for the same key.
func (m *Manager) Invalidate(key interface{}) {
	m.mu.Lock()
	delete(m.rounds, key)
	m.mu.Unlock()
}

// run executes fn for a fresh round and finishes the round with its result.
// A panic in fn is converted into a round failure (so waiters are released
// and the key stays retryable) and then re-panicked.
func (m *Manager) run(key interface{}, r *round, ctx context.Context, fn InitFunc) (val interface{}, err error) {
	defer func() {
		if p := recover(); p != nil {
			m.finish(key, r, nil, fmt.Errorf("keyinit: init function panicked: %v", p))
			panic(p)
		}
	}()
	val, err = fn(withChain(ctx, key))
	m.finish(key, r, val, err)
	return val, err
}

// finish publishes the round result to its waiters and updates the cache.
// If the round is no longer registered for key (invalidated while in
// flight), the result is discarded instead of stored.
func (m *Manager) finish(key interface{}, r *round, val interface{}, err error) {
	m.mu.Lock()
	r.val, r.err = val, err
	if m.rounds[key] == r {
		if err != nil {
			// Failure: keep nothing, so the next Do retries.
			delete(m.rounds, key)
		}
		// Success: keep the completed round as the cached value.
	}
	close(r.done)
	m.mu.Unlock()
}

// chainKey is the context key for the chain of keys being initialized.
type chainKey struct{}

// withChain returns a context that records key as being initialized.
func withChain(ctx context.Context, key interface{}) context.Context {
	chain, _ := ctx.Value(chainKey{}).([]interface{})
	next := make([]interface{}, len(chain)+1)
	copy(next, chain)
	next[len(chain)] = key
	return context.WithValue(ctx, chainKey{}, next)
}

// inChain reports whether key is already being initialized in ctx's chain.
func inChain(ctx context.Context, key interface{}) bool {
	chain, _ := ctx.Value(chainKey{}).([]interface{})
	for _, k := range chain {
		if k == key {
			return true
		}
	}
	return false
}
