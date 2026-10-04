// Package swrcache implements a stale-while-revalidate cache with
// single-flight refresh, generation-checked invalidation, per-caller
// staleness windows and failure backoff.
package swrcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRefreshBackoff is returned (together with the best-effort stale value)
// when the caller's staleness window rejects the cached value but a failing
// refresh is still backing off.
var ErrRefreshBackoff = errors.New("swrcache: refresh suppressed by failure backoff")

// errInvalidated marks a load whose result was discarded because the key was
// invalidated while the load was in flight.
var errInvalidated = errors.New("swrcache: load discarded by invalidation")

// Loader fetches the fresh value for a key.
type Loader func(ctx context.Context, key string) (interface{}, error)

// Options configures a Cache.
type Options struct {
	// TTL is how long a loaded value stays fresh.
	TTL time.Duration
	// InitialBackoff is the retry delay after the first refresh failure.
	// It doubles on each consecutive failure up to MaxBackoff.
	InitialBackoff time.Duration
	// MaxBackoff caps the failure backoff.
	MaxBackoff time.Duration
}

// Cache is a stale-while-revalidate cache. It is safe for concurrent use.
type Cache struct {
	loader Loader
	opts   Options
	clock  Clock

	mu       sync.Mutex
	entries  map[string]*entry
	versions map[string]uint64 // generation per key, bumped on Invalidate

	// onLoadDone, when set, is invoked after every load settles. Test seam.
	onLoadDone func()
}

type entry struct {
	value       interface{}
	expiresAt   time.Time
	refreshing  bool          // single-flight: only one load in flight per key
	done        chan struct{} // closed when the in-flight load settles
	failures    int           // consecutive refresh failures
	nextRetryAt time.Time     // failure backoff: no refresh before this time
}

// New creates a Cache. A nil clock uses wall time.
func New(loader Loader, opts Options, clock Clock) *Cache {
	if clock == nil {
		clock = realClock{}
	}
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = 100 * time.Millisecond
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	return &Cache{
		loader:   loader,
		opts:     opts,
		clock:    clock,
		entries:  make(map[string]*entry),
		versions: make(map[string]uint64),
	}
}

// Get returns the value for key.
//
//   - Fresh value (age < TTL): returned immediately.
//   - Stale but within maxStale: returned immediately; a background refresh
//     is triggered unless one is already in flight or backing off.
//   - Stale beyond maxStale (or absent): the caller waits for a refresh,
//     joining an in-flight one if any (single-flight).
//
// maxStale is per-call, so different callers tolerate different staleness.
// On refresh failure with no acceptable value, Get returns the loader error,
// or ErrRefreshBackoff (with the too-stale value as best effort) while a
// failing refresh is backing off.
func (c *Cache) Get(ctx context.Context, key string, maxStale time.Duration) (interface{}, error) {
	for {
		c.mu.Lock()
		now := c.clock.Now()
		e := c.entries[key]

		if e != nil && now.Before(e.expiresAt) {
			v := e.value
			c.mu.Unlock()
			return v, nil
		}

		if e != nil && now.Before(e.expiresAt.Add(maxStale)) {
			// Acceptable staleness: serve stale, kick a background refresh.
			c.maybeRefreshLocked(key, e, now)
			v := e.value
			c.mu.Unlock()
			return v, nil
		}

		// No acceptable value: need a fresh one.
		if e != nil && e.refreshing {
			done := e.done
			c.mu.Unlock()
			select {
			case <-done:
				continue // refresh settled, re-evaluate
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if e != nil && now.Before(e.nextRetryAt) {
			v := e.value
			c.mu.Unlock()
			return v, ErrRefreshBackoff
		}

		// Become the (single) refresher and load synchronously.
		ver := c.versions[key]
		if e == nil {
			e = &entry{}
			c.entries[key] = e
		}
		e.refreshing = true
		e.done = make(chan struct{})
		c.mu.Unlock()

		err := c.load(ctx, key, ver)
		if err == errInvalidated {
			continue // a newer generation owns the key now
		}
		if err != nil {
			return nil, err
		}
		// Load succeeded; loop to read the fresh value.
	}
}

// Invalidate drops the cached value and bumps the key's generation, so a
// refresh started before the invalidation cannot repopulate the entry.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.versions[key]++
	if e := c.entries[key]; e != nil && e.refreshing {
		// Wake waiters; the in-flight load's result will be discarded by
		// the generation check in load.
		close(e.done)
	}
	delete(c.entries, key)
}

// maybeRefreshLocked starts a background refresh if none is in flight and
// the failure backoff has elapsed. Caller must hold c.mu.
func (c *Cache) maybeRefreshLocked(key string, e *entry, now time.Time) {
	if e.refreshing || now.Before(e.nextRetryAt) {
		return
	}
	e.refreshing = true
	e.done = make(chan struct{})
	ver := c.versions[key]
	go func() { _ = c.load(context.Background(), key, ver) }()
}

// load runs the loader and settles the entry. The result is discarded if the
// key's generation changed (invalidation) while the load was in flight.
func (c *Cache) load(ctx context.Context, key string, ver uint64) error {
	v, lerr := c.loader(ctx, key)

	now := c.clock.Now()
	c.mu.Lock()
	if c.versions[key] != ver {
		// Invalidated (or superseded) while loading: drop the result and
		// leave the newer generation's entry untouched.
		c.mu.Unlock()
		c.notifyLoadDone()
		return errInvalidated
	}
	e := c.entries[key]
	if lerr != nil {
		e.failures++
		backoff := c.opts.InitialBackoff << uint(e.failures-1)
		if backoff > c.opts.MaxBackoff || backoff < 0 {
			backoff = c.opts.MaxBackoff
		}
		e.nextRetryAt = now.Add(backoff)
	} else {
		e.value = v
		e.expiresAt = now.Add(c.opts.TTL)
		e.failures = 0
		e.nextRetryAt = time.Time{}
	}
	e.refreshing = false
	close(e.done)
	c.mu.Unlock()
	c.notifyLoadDone()
	return lerr
}

func (c *Cache) notifyLoadDone() {
	if c.onLoadDone != nil {
		c.onLoadDone()
	}
}
