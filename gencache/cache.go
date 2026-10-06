// Package gencache provides a concurrent loading cache with generational
// invalidation.
//
// For a given key, concurrent Get calls within the same generation share a
// single invocation of the loader. Invalidate retires the current generation:
// an in-flight load belonging to the retired generation still delivers its
// result to its original waiters, but is never published to the new
// generation. Cancelling one waiter's context does not affect the others;
// the load itself is cancelled only once its last waiter has cancelled.
package gencache

import (
	"context"
	"sync"
)

// Loader fetches the value for key. The provided context is cancelled when
// the load's last waiter cancels, or when the load's generation is retired
// with no remaining waiters.
type Loader[K comparable, V any] func(ctx context.Context, key K) (V, error)

// entry is one generation of a key: either an in-flight load or its result.
type entry[V any] struct {
	done    chan struct{} // closed when the load finishes
	val     V
	err     error
	loaded  bool // guarded by Cache.mu
	waiters int  // guarded by Cache.mu
	cancel  context.CancelFunc
}

// Cache maps keys to lazily loaded values.
type Cache[K comparable, V any] struct {
	loader Loader[K, V]
	mu     sync.Mutex
	items  map[K]*entry[V]
}

// New returns a Cache that loads missing values with loader.
func New[K comparable, V any](loader Loader[K, V]) *Cache[K, V] {
	return &Cache[K, V]{loader: loader, items: make(map[K]*entry[V])}
}

// Get returns the value for key, joining the in-flight load of the current
// generation if there is one. The loader is invoked at most once per key per
// generation. If ctx is cancelled while waiting, Get returns ctx.Err()
// without affecting other waiters; the underlying load is cancelled only
// when its last waiter cancels.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, error) {
	c.mu.Lock()
	e, ok := c.items[key]
	if !ok {
		loadCtx, cancel := context.WithCancel(context.Background())
		e = &entry[V]{done: make(chan struct{}), cancel: cancel}
		c.items[key] = e
		go c.load(loadCtx, key, e)
	}
	e.waiters++
	c.mu.Unlock()

	select {
	case <-e.done:
		c.mu.Lock()
		e.waiters--
		c.mu.Unlock()
		return e.val, e.err
	case <-ctx.Done():
		c.mu.Lock()
		e.waiters--
		if e.waiters == 0 && !e.loaded {
			// Last waiter left before the load finished: cancel the
			// load and retire the entry so the next Get reloads.
			e.cancel()
			if c.items[key] == e {
				delete(c.items, key)
			}
		}
		c.mu.Unlock()
		var zero V
		return zero, ctx.Err()
	}
}

// Invalidate retires the current generation of key. An in-flight load keeps
// running and still delivers its result to its original waiters, but the
// result is not published: the next Get starts a new generation with a fresh
// loader invocation.
func (c *Cache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

func (c *Cache[K, V]) load(ctx context.Context, key K, e *entry[V]) {
	v, err := c.loader(ctx, key)
	c.mu.Lock()
	e.val, e.err = v, err
	e.loaded = true
	close(e.done)
	c.mu.Unlock()
}
