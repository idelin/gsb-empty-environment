// Package hotcache implements a single-flight loading cache with
// generation-based invalidation.
//
// Semantics:
//   - For a given key and generation the loader is invoked at most once;
//     concurrent Get calls share the in-flight load.
//   - Invalidate bumps the key's generation. A load started before the
//     invalidation still delivers its result to its own waiters, but is
//     detached and never written into the new generation.
//   - A waiter's context cancellation only affects that waiter. The load
//     itself is cancelled only after every waiter has cancelled.
package hotcache

import (
	"context"
	"sync"
)

// LoaderFunc loads the value for key. Its context is cancelled only when
// every waiter of the load has cancelled.
type LoaderFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Cache is a loading cache. The zero value is not usable; use New.
type Cache[K comparable, V any] struct {
	mu      sync.Mutex
	loader  LoaderFunc[K, V]
	entries map[K]*entry[K, V]
}

type entry[K comparable, V any] struct {
	gen      uint64
	value    V
	ok       bool
	inflight *call[K, V]
}

// call is one in-flight load shared by its waiters.
type call[K comparable, V any] struct {
	gen     uint64
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	value   V
	err     error
}

// New returns a Cache that loads missing values with loader.
func New[K comparable, V any](loader LoaderFunc[K, V]) *Cache[K, V] {
	return &Cache[K, V]{loader: loader, entries: make(map[K]*entry[K, V])}
}

// Get returns the cached value for key, joining an in-flight load for the
// current generation or starting a new one.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, error) {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &entry[K, V]{}
		c.entries[key] = e
	}
	if e.ok {
		v := e.value
		c.mu.Unlock()
		return v, nil
	}
	cl := e.inflight
	if cl != nil {
		select {
		case <-cl.ctx.Done():
			// The load was cancelled after its last waiter left but
			// has not finished unwinding; never join a dead call.
			cl = nil
		default:
		}
	}
	if cl == nil {
		cl = newCall[K, V](e.gen)
		e.inflight = cl
		go c.run(cl, key)
	}
	cl.waiters++
	c.mu.Unlock()

	select {
	case <-cl.done:
		c.mu.Lock()
		cl.waiters--
		c.mu.Unlock()
		return cl.value, cl.err
	case <-ctx.Done():
		c.mu.Lock()
		cl.waiters--
		if cl.waiters == 0 {
			select {
			case <-cl.done:
			default:
				cl.cancel()
			}
		}
		c.mu.Unlock()
		var zero V
		return zero, ctx.Err()
	}
}

func (c *Cache[K, V]) run(cl *call[K, V], key K) {
	v, err := c.loader(cl.ctx, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	cl.value, cl.err = v, err
	close(cl.done)
	// Generation isolation: if the entry was invalidated while this load
	// was in flight, e.inflight no longer points at cl and the stale
	// result is delivered to cl's waiters but never stored.
	if e := c.entries[key]; e != nil && e.inflight == cl {
		if err == nil {
			e.value, e.ok = v, true
		}
		e.inflight = nil
	}
}

// Invalidate drops the cached value for key and advances its generation.
// An in-flight load from the old generation keeps running for its own
// waiters but is detached so it cannot populate the new generation.
func (c *Cache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		e.gen++
		e.ok = false
		e.inflight = nil
	}
	c.mu.Unlock()
}

func newCall[K comparable, V any](gen uint64) *call[K, V] {
	ctx, cancel := context.WithCancel(context.Background())
	return &call[K, V]{gen: gen, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
