// Package leasecache implements an in-process cache of borrowable resources.
//
// Cached values are returnable objects: callers borrow a Lease and must
// Release it exactly once. The cache itself holds one reference while the
// value is cached; eviction (explicit or TTL-based) only drops the cache's
// own reference, so objects still borrowed stay alive for their borrowers
// and are never handed out to other callers again. Close drops the cache's
// references and recycles delayed objects as borrowers return them.
//
// The data source (Loader), the clock (Now) and failure injection (Failer)
// are all injectable functions; there is no remote cache dependency.
package leasecache

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed is returned by Get after the cache has been closed.
	ErrClosed = errors.New("leasecache: cache is closed")
	// ErrDoubleRelease is returned when a lease is released more than once.
	ErrDoubleRelease = errors.New("leasecache: lease already released")
)

// Op identifies an operation at which a fault can be injected.
type Op string

const (
	// OpLoad is consulted before each call to the data source.
	OpLoad Op = "load"
)

// Options configures a Cache. Loader is required; everything else is optional.
type Options[K comparable, V any] struct {
	// Loader is the injectable data source. It is called on cache miss and
	// its error is returned to the caller without leaking any pool object.
	Loader func(ctx context.Context, key K) (V, error)

	// Destroy recycles a value once its last reference is gone. It is
	// always called exactly once per successfully loaded value.
	Destroy func(v V)

	// TTL, if positive, makes entries expire; expiry is evaluated lazily
	// on Get using Now.
	TTL time.Duration

	// Now is the injectable clock. Defaults to time.Now.
	Now func() time.Time

	// Failer, if non-nil, is consulted before each load; returning an
	// error simulates a backend failure without touching the pool.
	Failer func(op Op) error
}

type entry[V any] struct {
	val    V
	refs   int  // cache hold (while cached) + outstanding leases
	cached bool // the cache still holds its reference
	expire time.Time
}

// Cache is a concurrency-safe cache of borrowable values.
type Cache[K comparable, V any] struct {
	loader  func(ctx context.Context, key K) (V, error)
	destroy func(v V)
	ttl     time.Duration
	now     func() time.Time
	failer  func(op Op) error

	mu     sync.Mutex
	items  map[K]*entry[V]
	closed bool
	wg     sync.WaitGroup // one count per live (not yet destroyed) entry
}

// New builds a Cache from opts. It panics if opts.Loader is nil.
func New[K comparable, V any](opts Options[K, V]) *Cache[K, V] {
	if opts.Loader == nil {
		panic("leasecache: Options.Loader is required")
	}
	c := &Cache[K, V]{
		loader:  opts.Loader,
		destroy: opts.Destroy,
		ttl:     opts.TTL,
		now:     opts.Now,
		failer:  opts.Failer,
		items:   make(map[K]*entry[V]),
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.destroy == nil {
		c.destroy = func(V) {}
	}
	return c
}

// Get returns a lease for key, loading the value from the data source on a
// cache miss. The caller must Release the lease exactly once. A value that
// was evicted or expired while borrowed is never reused for other callers;
// a fresh value is loaded instead.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (*Lease[K, V], error) {
	var expired *entry[V]

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if e, ok := c.items[key]; ok {
		fresh := e.expire.IsZero() || c.now().Before(e.expire)
		if fresh {
			e.refs++
			c.mu.Unlock()
			return &Lease[K, V]{cache: c, entry: e, val: e.val}, nil
		}
		// Lazy expiry: drop the cache's hold only; borrowers keep theirs.
		delete(c.items, key)
		e.cached = false
		e.refs--
		if e.refs == 0 {
			expired = e
		}
	}
	c.mu.Unlock()
	if expired != nil {
		c.destroyEntry(expired)
	}

	if c.failer != nil {
		if err := c.failer(OpLoad); err != nil {
			return nil, err
		}
	}
	val, err := c.loader(ctx, key)
	if err != nil {
		// Nothing was inserted into the pool: no object can leak.
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.destroy(val) // loaded while closing: recycle immediately
		return nil, ErrClosed
	}
	if existing, ok := c.items[key]; ok {
		// A concurrent load won the race; recycle ours and borrow theirs.
		existing.refs++
		c.mu.Unlock()
		c.destroy(val)
		return &Lease[K, V]{cache: c, entry: existing, val: existing.val}, nil
	}
	e := &entry[V]{val: val, refs: 2, cached: true} // cache hold + this lease
	if c.ttl > 0 {
		e.expire = c.now().Add(c.ttl)
	}
	c.items[key] = e
	c.wg.Add(1)
	c.mu.Unlock()
	return &Lease[K, V]{cache: c, entry: e, val: val}, nil
}

// Evict drops the cache's hold on key. Borrowers keep their leases valid;
// the object is recycled when its last borrower releases it, and is never
// reused for other callers.
func (c *Cache[K, V]) Evict(key K) {
	c.mu.Lock()
	e, ok := c.items[key]
	if ok {
		delete(c.items, key)
		e.cached = false
		e.refs--
	}
	c.mu.Unlock()
	if ok && e.refs == 0 {
		c.destroyEntry(e)
	}
}

// Close drops the cache's hold on every cached value. Unborrowed values are
// recycled immediately; values still on loan are recycled lazily as their
// borrowers release them (delayed reclamation). Use Wait to block until all
// delayed objects have been recycled.
func (c *Cache[K, V]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	doomed := make([]*entry[V], 0, len(c.items))
	for key, e := range c.items {
		delete(c.items, key)
		e.cached = false
		e.refs--
		if e.refs == 0 {
			doomed = append(doomed, e)
		}
	}
	c.mu.Unlock()
	for _, e := range doomed {
		c.destroyEntry(e)
	}
}

// Wait blocks until every loaded value has been recycled. Call it after
// Close once all borrowers are expected to have released their leases.
func (c *Cache[K, V]) Wait() { c.wg.Wait() }

// Len reports how many values the cache currently holds (borrowed or not).
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// destroyEntry recycles an entry whose reference count reached zero.
func (c *Cache[K, V]) destroyEntry(e *entry[V]) {
	c.destroy(e.val)
	c.wg.Done()
}

// Lease is a borrow handle on a cached value. It must be released exactly
// once; the value must not be used after Release.
type Lease[K comparable, V any] struct {
	cache    *Cache[K, V]
	entry    *entry[V]
	val      V
	released bool // guarded by cache.mu
}

// Value returns the borrowed object. It stays valid until Release.
func (l *Lease[K, V]) Value() V { return l.val }

// Release returns the borrowed object. The second and any subsequent call
// returns ErrDoubleRelease and has no effect.
func (l *Lease[K, V]) Release() error {
	c := l.cache
	c.mu.Lock()
	if l.released {
		c.mu.Unlock()
		return ErrDoubleRelease
	}
	l.released = true
	e := l.entry
	e.refs--
	last := e.refs == 0
	c.mu.Unlock()
	if last {
		c.destroyEntry(e)
	}
	return nil
}
