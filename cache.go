// Package ttlcache implements a key/value cache with soft and hard TTLs.
//
// Semantics:
//   - Before the soft TTL a read returns the cached value.
//   - Between soft and hard TTL a read returns the stale value and triggers
//     a single shared background refresh (single-flight).
//   - After the hard TTL a read must wait for a fresh value; stale values are
//     never served past the hard deadline, and failure backoff never extends
//     the hard deadline.
//
// Linearization points:
//   - A fresh/stale read linearizes under the cache mutex at the moment the
//     entry state is inspected.
//   - A hard-expired read linearizes at the refresh completion that publishes
//     the value it returns (it waits on the refresh's done channel).
//   - A refresh result linearizes under the mutex when it is applied to the
//     entry; results whose entry generation no longer matches (key deleted or
//     overwritten via Set) are discarded, so a late refresh can neither
//     resurrect a deleted key nor overwrite a newer value.
//   - Reader cancellation linearizes at the select on ctx.Done(): the
//     in-flight shared refresh is unaffected and continues for other readers.
//   - Close linearizes under the mutex; it cancels in-flight refreshes and
//     blocks until every borrowed resource (refresh goroutine) is released.
package ttlcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClosed is returned by Get after Close has been called.
var ErrClosed = errors.New("ttlcache: cache is closed")

// Source loads a fresh value for key. The ctx is canceled on Close.
type Source[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Backoff returns the delay before the next background refresh attempt after
// the given number of consecutive failures. It only throttles soft-window
// refreshes; it never delays a refresh required by a hard-expired read.
type Backoff func(failures int) time.Duration

// Result is the outcome of a Get.
type Result[V any] struct {
	Value V
	// Version is the source version of the value: 1 for the first successful
	// load, incremented on every successful refresh or Set.
	Version uint64
	// Stale reports whether the value was served past its soft TTL.
	Stale bool
	// UsableUntil is the hard deadline: the value must not be used after it.
	UsableUntil time.Time
}

// Config configures a Cache. Source is required. Now defaults to time.Now;
// Backoff defaults to 100ms << (failures-1), capped at 30s.
type Config[K comparable, V any] struct {
	Source  Source[K, V]
	SoftTTL time.Duration
	HardTTL time.Duration
	Now     func() time.Time
	Backoff Backoff
}

type entry[V any] struct {
	value    V
	version  uint64
	hasValue bool
	softAt   time.Time
	hardAt   time.Time

	// refreshing/done implement single-flight: done is closed exactly once,
	// when the in-flight refresh publishes or drops its result.
	refreshing bool
	done       chan struct{}

	// failures/nextAttempt implement failure backoff for soft-window
	// refreshes only.
	failures    int
	nextAttempt time.Time
}

// Cache is a soft/hard TTL cache. The zero value is not usable; use New.
type Cache[K comparable, V any] struct {
	source  Source[K, V]
	softTTL time.Duration
	hardTTL time.Duration
	now     func() time.Time
	backoff Backoff

	mu      sync.Mutex
	entries map[K]*entry[V]
	closed  bool

	ctx    context.Context
	cancel context.CancelFunc

	wg       sync.WaitGroup // in-flight refresh goroutines
	inflight atomic.Int32   // borrowed-resource gauge, for tests/observability
}

// New creates a Cache. It panics if Source or the TTLs are invalid.
func New[K comparable, V any](cfg Config[K, V]) *Cache[K, V] {
	if cfg.Source == nil {
		panic("ttlcache: nil Source")
	}
	if cfg.SoftTTL <= 0 || cfg.HardTTL <= cfg.SoftTTL {
		panic("ttlcache: require 0 < SoftTTL < HardTTL")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = func(failures int) time.Duration {
			d := 100 * time.Millisecond << (failures - 1)
			if d > 30*time.Second || d < 0 {
				d = 30 * time.Second
			}
			return d
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache[K, V]{
		source:  cfg.Source,
		softTTL: cfg.SoftTTL,
		hardTTL: cfg.HardTTL,
		now:     now,
		backoff: backoff,
		entries: make(map[K]*entry[V]),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Get returns the value for key, loading or refreshing it as needed.
//
// A soft-expired value is returned immediately (Stale=true) while a shared
// background refresh runs. A hard-expired key blocks until a refresh
// publishes a fresh value, the cache is closed, or ctx is canceled.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (Result[V], error) {
	c.mu.Lock()
	for {
		if c.closed {
			c.mu.Unlock()
			return Result[V]{}, ErrClosed
		}
		e, ok := c.entries[key]
		if !ok {
			e = &entry[V]{}
			c.entries[key] = e
		}
		now := c.now()
		switch {
		case e.hasValue && now.Before(e.softAt):
			// Linearization point: fresh read.
			r := Result[V]{Value: e.value, Version: e.version, UsableUntil: e.hardAt}
			c.mu.Unlock()
			return r, nil
		case e.hasValue && now.Before(e.hardAt):
			// Linearization point: stale read. Kick off the shared refresh
			// unless one is in flight or failure backoff is still active.
			if !e.refreshing && !now.Before(e.nextAttempt) {
				c.startRefreshLocked(key, e)
			}
			r := Result[V]{Value: e.value, Version: e.version, Stale: true, UsableUntil: e.hardAt}
			c.mu.Unlock()
			return r, nil
		default:
			// Hard-expired (or never loaded): must wait for a fresh value.
			// Backoff is intentionally ignored here so it can never extend
			// the hard deadline.
			if !e.refreshing {
				c.startRefreshLocked(key, e)
			}
			done := e.done
			c.mu.Unlock()
			select {
			case <-done:
				c.mu.Lock()
			case <-ctx.Done():
				// Reader cancellation: the shared refresh keeps running for
				// other readers.
				return Result[V]{}, ctx.Err()
			}
		}
	}
}

// Set stores v as a fresh value, invalidating any in-flight refresh for key:
// a late refresh result can never overwrite this newer value.
func (c *Cache[K, V]) Set(key K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	version := uint64(1)
	if old, ok := c.entries[key]; ok {
		version = old.version + 1
	}
	c.entries[key] = &entry[V]{
		value:    v,
		version:  version,
		hasValue: true,
		softAt:   now.Add(c.softTTL),
		hardAt:   now.Add(c.hardTTL),
	}
}

// Delete removes key. An in-flight refresh for the old entry is discarded
// when it completes, so it cannot resurrect the deleted key.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Close marks the cache closed, cancels in-flight refreshes, and blocks until
// every refresh goroutine has exited. Get returns ErrClosed afterwards.
func (c *Cache[K, V]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
}

// InFlight reports the number of refresh goroutines currently running; it is
// the borrowed-resource gauge and must return to 0 after Close.
func (c *Cache[K, V]) InFlight() int {
	return int(c.inflight.Load())
}

// startRefreshLocked starts the single shared refresh for e.
// Callers must hold c.mu and have checked !e.refreshing.
func (c *Cache[K, V]) startRefreshLocked(key K, e *entry[V]) {
	e.refreshing = true
	e.done = make(chan struct{})
	c.wg.Add(1)
	c.inflight.Add(1)
	go c.refresh(key, e, e.done)
}

// refresh runs the source and publishes the result. The publication is the
// linearization point for waiting readers; results for a superseded entry
// (deleted, Set, or closed cache) are dropped.
func (c *Cache[K, V]) refresh(key K, e *entry[V], done chan struct{}) {
	defer c.wg.Done()
	defer c.inflight.Add(-1)

	v, err := c.source(c.ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()
	defer close(done) // wake hard-expired waiters even when the result is dropped

	if c.closed || c.entries[key] != e {
		return // stale generation: key deleted, overwritten, or cache closed
	}
	now := c.now()
	if err != nil {
		e.failures++
		e.nextAttempt = now.Add(c.backoff(e.failures))
		e.refreshing = false
		return
	}
	e.value = v
	e.hasValue = true
	e.version++
	e.softAt = now.Add(c.softTTL)
	e.hardAt = now.Add(c.hardTTL)
	e.failures = 0
	e.nextAttempt = time.Time{}
	e.refreshing = false
}
