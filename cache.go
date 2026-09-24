// Package leasecache implements an in-process cache whose values are
// borrowable resources. Callers borrow values through reference-counted
// leases; eviction and Close only drop the cache's own hold on a value,
// never disturbing active borrowers. A value is recycled exactly once,
// when its last reference is gone and the cache no longer holds it.
//
// The data source (Loader), the clock, and resource teardown (Recycler)
// are all injected as functions, so tests can simulate failures and
// control time without any remote cache.
package leasecache

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed is returned by Acquire after the cache has been closed.
	ErrClosed = errors.New("leasecache: cache is closed")
	// ErrReleased is returned when a lease is released more than once.
	ErrReleased = errors.New("leasecache: lease already released")
)

// Loader fetches a fresh value for key from the data source. A returned
// error is propagated to the caller and nothing is cached or leaked.
type Loader[K comparable, V any] func(key K) (V, error)

// Recycler tears down a value. It is invoked exactly once per loaded
// value, after the cache's hold and all outstanding leases are gone.
type Recycler[V any] func(v V)

// Config carries optional knobs. The zero value is valid.
type Config struct {
	// TTL, when > 0, makes entries expire; expired entries are evicted
	// lazily on the next Acquire of the same key.
	TTL time.Duration
	// Clock supplies the current time; nil means time.Now.
	Clock func() time.Time
}

type entry[V any] struct {
	value     V
	refs      int  // outstanding leases
	detached  bool // cache no longer holds this entry (evicted/expired/closed)
	expiresAt time.Time
}

// Cache is a concurrency-safe borrowable-object cache.
type Cache[K comparable, V any] struct {
	load    Loader[K, V]
	recycle Recycler[V]
	now     func() time.Time
	ttl     time.Duration

	mu      sync.Mutex
	entries map[K]*entry[V]
	closed  bool

	// Counters, guarded by mu, exported through Stats for tests and
	// resource-release verification.
	loaded   int // values successfully loaded
	recycled int // values handed to the Recycler
}

// New builds a Cache. recycler may be nil if values need no teardown.
func New[K comparable, V any](loader Loader[K, V], recycler Recycler[V], cfg Config) *Cache[K, V] {
	now := cfg.Clock
	if now == nil {
		now = time.Now
	}
	if recycler == nil {
		recycler = func(V) {}
	}
	return &Cache[K, V]{
		load:    loader,
		recycle: recycler,
		now:     now,
		ttl:     cfg.TTL,
		entries: make(map[K]*entry[V]),
	}
}

// Lease is a borrow handle on a cached value. It must be released exactly
// once; the second Release returns ErrReleased and is otherwise a no-op.
type Lease[K comparable, V any] struct {
	cache *Cache[K, V]
	entry *entry[V]

	mu       sync.Mutex
	released bool
}

// Value returns the borrowed object. It remains valid to use until
// Release is called, even if the entry was evicted or the cache closed.
func (l *Lease[K, V]) Value() V { return l.entry.value }

// Release returns the borrowed object. Releasing twice is rejected.
func (l *Lease[K, V]) Release() error {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return ErrReleased
	}
	l.released = true
	l.mu.Unlock()
	l.cache.release(l.entry)
	return nil
}

// Acquire returns a lease on the value for key, loading it from the data
// source on a cache miss. Concurrent borrowers of a live entry share the
// same object. An entry that was evicted or expired is never handed out
// again, even while earlier borrowers still hold it.
func (c *Cache[K, V]) Acquire(key K) (*Lease[K, V], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if e, ok := c.entries[key]; ok {
		if c.expiredLocked(e) {
			// Lazy expiry: detach now; borrowers keep their object.
			delete(c.entries, key)
			e.detached = true
			c.collectLocked(e)
		} else {
			e.refs++
			return &Lease[K, V]{cache: c, entry: e}, nil
		}
	}
	v, err := c.load(key)
	if err != nil {
		// Nothing stored, nothing to recycle: no pool object leaks.
		return nil, err
	}
	e := &entry[V]{value: v, refs: 1}
	if c.ttl > 0 {
		e.expiresAt = c.now().Add(c.ttl)
	}
	c.entries[key] = e
	c.loaded++
	return &Lease[K, V]{cache: c, entry: e}, nil
}

// Evict drops the cache's hold on key. Active borrowers are unaffected;
// their object is recycled when the last lease is released, and is never
// reused for a later Acquire.
func (c *Cache[K, V]) Evict(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		delete(c.entries, key)
		e.detached = true
		c.collectLocked(e)
	}
}

// Close evicts every entry and rejects future Acquires. Idle values are
// recycled immediately; values still on loan are recycled lazily when
// their last lease is released (delayed reclamation).
func (c *Cache[K, V]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.closed = true
	for key, e := range c.entries {
		delete(c.entries, key)
		e.detached = true
		c.collectLocked(e)
	}
	return nil
}

// release drops one lease reference and recycles the value if the entry
// is detached and this was the last reference.
func (c *Cache[K, V]) release(e *entry[V]) {
	c.mu.Lock()
	e.refs--
	c.collectLocked(e)
	c.mu.Unlock()
}

// collectLocked recycles e's value when nothing holds it any more.
// Must be called with c.mu held.
func (c *Cache[K, V]) collectLocked(e *entry[V]) {
	if e.detached && e.refs == 0 {
		e.refs = -1 // guard: collect at most once
		c.recycled++
		// Recycler runs under the lock; it must not re-enter the cache.
		c.recycle(e.value)
	}
}

func (c *Cache[K, V]) expiredLocked(e *entry[V]) bool {
	return c.ttl > 0 && !c.now().Before(e.expiresAt)
}

// Stats reports how many values were loaded and recycled. When every
// lease is released and the cache is closed, Loaded == Recycled proves
// no resource leaked.
type Stats struct {
	Loaded   int
	Recycled int
}

// Stats returns a snapshot of load/recycle counters.
func (c *Cache[K, V]) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Loaded: c.loaded, Recycled: c.recycled}
}

// Len reports how many entries the cache currently holds.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
