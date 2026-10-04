// Package srwcache implements a stale-while-revalidate cache:
//
//   - entries serve stale values for a caller-chosen window after expiry;
//   - at most one refresh per key is in flight (single-flight);
//   - Invalidate bumps a per-key version so a refresh that started before
//     the invalidation can never re-store its (now stale) result;
//   - consecutive refresh failures back off exponentially.
package srwcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRefreshBackoff is returned when a caller requires a fresh value but the
// loader is in a failure-backoff period and no acceptable stale value exists.
var ErrRefreshBackoff = errors.New("srwcache: refresh suppressed by failure backoff")

// Loader fetches the current value for a key.
type Loader func(ctx context.Context, key string) (interface{}, error)

// Options configures a Cache.
type Options struct {
	// TTL is how long a loaded value stays fresh. After TTL a value is
	// stale; callers decide per-Get how much staleness they tolerate.
	TTL time.Duration

	// Loader loads values on cache miss / expiry. Required.
	Loader Loader

	// Clock overrides the time source (defaults to wall clock).
	Clock Clock

	// BackoffBase is the initial backoff after a refresh failure
	// (default 100ms). It doubles per consecutive failure up to BackoffMax.
	BackoffBase time.Duration

	// BackoffMax caps the failure backoff (default 30s).
	BackoffMax time.Duration
}

type entry struct {
	value    interface{}
	loadedAt time.Time
}

// refreshCall represents one in-flight refresh. done is closed when the
// refresh finishes (success or failure). version is the invalidation
// version captured when the refresh started.
type refreshCall struct {
	done    chan struct{}
	version uint64
}

// Cache is a stale-while-revalidate cache. It is safe for concurrent use.
type Cache struct {
	loader      Loader
	ttl         time.Duration
	clock       Clock
	backoffBase time.Duration
	backoffMax  time.Duration

	mu           sync.Mutex
	entries      map[string]*entry
	versions     map[string]uint64 // invalidation version per key
	inflight     map[string]*refreshCall
	failures     map[string]int
	backoffUntil map[string]time.Time
	lastErr      map[string]error
}

// New creates a Cache.
func New(opts Options) *Cache {
	if opts.Loader == nil {
		panic("srwcache: Options.Loader is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	base := opts.BackoffBase
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	max := opts.BackoffMax
	if max <= 0 {
		max = 30 * time.Second
	}
	return &Cache{
		loader:       opts.Loader,
		ttl:          opts.TTL,
		clock:        clock,
		backoffBase:  base,
		backoffMax:   max,
		entries:      make(map[string]*entry),
		versions:     make(map[string]uint64),
		inflight:     make(map[string]*refreshCall),
		failures:     make(map[string]int),
		backoffUntil: make(map[string]time.Time),
		lastErr:      make(map[string]error),
	}
}

// Get returns the value for key.
//
// If the cached value is fresh it is returned directly. If it is stale but
// no older than maxStale past expiry, the stale value is returned and a
// background refresh is triggered (unless one is already running or the
// loader is backing off). Otherwise Get refreshes synchronously, joining any
// refresh already in flight so the loader runs at most once per key.
func (c *Cache) Get(ctx context.Context, key string, maxStale time.Duration) (interface{}, error) {
	for {
		c.mu.Lock()
		now := c.clock.Now()
		e, ok := c.entries[key]
		if ok {
			age := now.Sub(e.loadedAt)
			if age <= c.ttl {
				v := e.value
				c.mu.Unlock()
				return v, nil
			}
			if age <= c.ttl+maxStale {
				// Stale but acceptable for this caller: serve it and
				// kick off a background refresh if allowed.
				c.startRefreshLocked(key, now, true)
				v := e.value
				c.mu.Unlock()
				return v, nil
			}
		}

		// A fresh value is required. Join an in-flight refresh if any.
		if call, flying := c.inflight[key]; flying {
			done := call.done
			c.mu.Unlock()
			select {
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}

		// Respect failure backoff: serve stale if this caller accepts it,
		// otherwise surface the last refresh error.
		if until, backing := c.backoffUntil[key]; backing && now.Before(until) {
			if ok {
				age := now.Sub(e.loadedAt)
				if age <= c.ttl+maxStale {
					v := e.value
					c.mu.Unlock()
					return v, nil
				}
			}
			err := c.lastErr[key]
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, ErrRefreshBackoff
		}

		// Become the single refresher for this key.
		call := &refreshCall{done: make(chan struct{}), version: c.versions[key]}
		c.inflight[key] = call
		c.mu.Unlock()

		c.refresh(ctx, key, call)
		// Loop to read the (possibly stored) result.
	}
}

// Invalidate deletes the cached value for key and bumps its version, so any
// refresh that started before this call cannot re-store its result.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.versions[key]++
}

// startRefreshLocked begins a refresh if none is in flight and the loader is
// not backing off. If async is true the refresh runs in a new goroutine;
// otherwise the caller is expected to run it itself via refresh.
// Callers must hold c.mu.
func (c *Cache) startRefreshLocked(key string, now time.Time, async bool) *refreshCall {
	if _, flying := c.inflight[key]; flying {
		return nil
	}
	if until, backing := c.backoffUntil[key]; backing && now.Before(until) {
		return nil
	}
	call := &refreshCall{done: make(chan struct{}), version: c.versions[key]}
	c.inflight[key] = call
	if async {
		go c.refresh(context.Background(), key, call)
	}
	return call
}

// refresh runs the loader once and publishes the outcome. The result is only
// stored if the key's invalidation version is unchanged since the refresh
// started; otherwise an Invalidate raced with us and the value is discarded.
func (c *Cache) refresh(ctx context.Context, key string, call *refreshCall) {
	val, err := c.loader(ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, key)
	close(call.done)
	now := c.clock.Now()

	if err != nil {
		c.failures[key]++
		c.backoffUntil[key] = now.Add(c.backoff(c.failures[key]))
		c.lastErr[key] = err
		return
	}
	if c.versions[key] != call.version {
		// Invalidated while loading: do not re-store the stale result.
		return
	}
	c.entries[key] = &entry{value: val, loadedAt: now}
	c.failures[key] = 0
	delete(c.backoffUntil, key)
	delete(c.lastErr, key)
}

// backoff returns the delay for the given consecutive failure count.
func (c *Cache) backoff(failures int) time.Duration {
	d := c.backoffBase
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= c.backoffMax {
			return c.backoffMax
		}
	}
	return d
}
