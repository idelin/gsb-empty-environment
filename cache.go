// Package softcache implements a single-key-space cache with soft/hard
// expiry, coalesced background refreshes, failure backoff and an
// injectable clock.
//
// Semantics:
//   - now < softExp:           serve the cached value (fresh).
//   - softExp <= now < hardExp: serve the cached value marked Stale and
//     trigger one coalesced background refresh.
//   - now >= hardExp:          never serve the old value; wait for the
//     shared refresh and return the new value or an error.
//
// Linearization points (all under Cache.mu):
//   - Get fresh/stale hit: reading the entry under mu.
//   - Get hard miss: joining or creating the per-key refreshCall under mu
//     (this is what guarantees at most one shared refresh per key).
//   - Refresh commit: runRefresh re-checks the key generation and the
//     value version under mu before storing. Invalidate bumps the
//     generation under the same mu, so a refresh either commits entirely
//     before an invalidation (its result is then dropped) or entirely
//     after it (its result is stored). A late, older-version response can
//     never overwrite a newer stored value, and a refresh that races a
//     delete can never resurrect the key.
//   - Reader cancellation: a hard-miss reader selects on ctx.Done(); the
//     shared refresh is unaffected and still serves the other waiters.
//   - Close: sets closed under mu, releases every stored value, cancels
//     the refresh context and waits (wg) for in-flight refreshes, whose
//     late results are discarded and released.
package softcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed is returned by Get after Close.
	ErrClosed = errors.New("softcache: cache is closed")
	// ErrInvalidated is returned to waiters whose refresh was discarded
	// because the key was invalidated while the fetch was in flight.
	ErrInvalidated = errors.New("softcache: key invalidated during refresh")
	// ErrStaleResponse is returned to hard-miss waiters when the source
	// answered with an older version than the one already stored and the
	// stored value is past its hard expiry.
	ErrStaleResponse = errors.New("softcache: source returned an older version")
	// ErrBackoff is returned on the hard path while a previous failure's
	// backoff window is still open and no last error is recorded.
	ErrBackoff = errors.New("softcache: refresh suppressed by failure backoff")
)

// SourceFunc loads a fresh value for key. The returned version must be
// monotonically increasing per key; it lets the cache reject late,
// older responses. The ctx is cancelled on Close.
type SourceFunc func(ctx context.Context, key string) (value interface{}, version uint64, err error)

// Config carries only injectable functions and durations: no remote
// cache client is involved.
type Config struct {
	// Source fetches fresh values. Required.
	Source SourceFunc
	// Now is the injectable clock. Defaults to time.Now.
	Now func() time.Time
	// SoftTTL is the fresh window; HardTTL is the absolute usable window.
	// HardTTL must be >= SoftTTL.
	SoftTTL time.Duration
	HardTTL time.Duration
	// Backoff maps the consecutive failure count (>=1) to the delay
	// before the next refresh attempt. Defaults to a constant 1s.
	Backoff func(failures int) time.Duration
	// Release, if set, is called exactly once for every value the cache
	// stops owning: overwritten entries, invalidated entries, entries
	// dropped on Close, and fetched values that are discarded (late or
	// stale responses). It is called under the cache lock and must not
	// call back into the Cache.
	Release func(value interface{})
}

// Result is a served value plus its provenance.
type Result struct {
	Value interface{}
	// Version is the source version of the served value.
	Version uint64
	// Stale is true when the value was served from the soft-expired
	// window while a refresh is pending.
	Stale bool
	// UsableUntil is the hard expiry of the served value: the instant
	// after which it must not be used.
	UsableUntil time.Time
}

type entry struct {
	value     interface{}
	version   uint64
	softExp   time.Time
	hardExp   time.Time
	fails     int
	nextRetry time.Time
	lastErr   error
}

// refreshCall is the single-flight handle shared by all hard-miss
// waiters of one key.
type refreshCall struct {
	done chan struct{}
	res  Result
	err  error
}

// Cache is a concurrent soft/hard TTL cache. The zero value is not
// usable; construct with New.
type Cache struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	entries  map[string]*entry
	gens     map[string]uint64 // per-key generation, survives deletes
	inflight map[string]*refreshCall
	closed   bool
	wg       sync.WaitGroup // in-flight refresh goroutines
}

// New builds a Cache from cfg.
func New(cfg Config) *Cache {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Backoff == nil {
		cfg.Backoff = func(int) time.Duration { return time.Second }
	}
	if cfg.HardTTL < cfg.SoftTTL {
		cfg.HardTTL = cfg.SoftTTL
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		entries:  make(map[string]*entry),
		gens:     make(map[string]uint64),
		inflight: make(map[string]*refreshCall),
	}
}

// Get returns the value for key according to the soft/hard semantics
// described in the package doc.
func (c *Cache) Get(ctx context.Context, key string) (Result, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Result{}, ErrClosed
	}
	now := c.cfg.Now()
	e, ok := c.entries[key]
	switch {
	case ok && now.Before(e.softExp):
		// Linearization: fresh hit.
		res := Result{Value: e.value, Version: e.version, UsableUntil: e.hardExp}
		c.mu.Unlock()
		return res, nil
	case ok && now.Before(e.hardExp):
		// Linearization: soft-expired hit. Serve stale and make sure
		// exactly one background refresh is running.
		res := Result{Value: e.value, Version: e.version, Stale: true, UsableUntil: e.hardExp}
		c.startRefreshLocked(key, now)
		c.mu.Unlock()
		return res, nil
	}
	// Hard miss (absent or past hard expiry): the old value must not be
	// served. Join or start the shared refresh and wait for it.
	call := c.startRefreshLocked(key, now)
	if call == nil {
		// Failure backoff is still open. Backoff never extends the hard
		// expiry, so report the recorded failure instead of the old value.
		err := ErrBackoff
		if ok && e.lastErr != nil {
			err = e.lastErr
		}
		c.mu.Unlock()
		return Result{}, err
	}
	c.mu.Unlock()
	select {
	case <-call.done:
		return call.res, call.err
	case <-ctx.Done():
		// Reader cancellation only abandons the wait; the shared refresh
		// keeps running for the remaining waiters.
		return Result{}, ctx.Err()
	}
}

// startRefreshLocked returns the in-flight call for key, starts a new
// one, or returns nil when failure backoff suppresses a new attempt.
func (c *Cache) startRefreshLocked(key string, now time.Time) *refreshCall {
	if call, ok := c.inflight[key]; ok {
		return call
	}
	if e, ok := c.entries[key]; ok && now.Before(e.nextRetry) {
		return nil
	}
	call := &refreshCall{done: make(chan struct{})}
	c.inflight[key] = call
	gen := c.gens[key]
	c.wg.Add(1)
	go c.runRefresh(key, gen, call)
	return call
}

func (c *Cache) runRefresh(key string, gen uint64, call *refreshCall) {
	defer c.wg.Done()
	value, version, err := c.cfg.Source(c.ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, key)
	now := c.cfg.Now()

	if err != nil {
		// Record backoff on the surviving entry only; a deleted key must
		// not be resurrected, not even by its failure state.
		if e, ok := c.entries[key]; ok && c.gens[key] == gen {
			e.fails++
			e.nextRetry = now.Add(c.cfg.Backoff(e.fails))
			e.lastErr = err
		}
		call.err = err
		close(call.done)
		return
	}

	// Linearization: the refresh commits here, iff the cache is still
	// open and no Invalidate/Delete raced us (generation unchanged).
	if c.closed {
		c.releaseLocked(value)
		call.err = ErrClosed
		close(call.done)
		return
	}
	if c.gens[key] != gen {
		c.releaseLocked(value)
		call.err = ErrInvalidated
		close(call.done)
		return
	}

	e, ok := c.entries[key]
	switch {
	case ok && version < e.version:
		// Late, older response: never overwrite a newer value.
		c.releaseLocked(value)
		if now.Before(e.hardExp) {
			call.res = Result{Value: e.value, Version: e.version, Stale: !now.Before(e.softExp), UsableUntil: e.hardExp}
		} else {
			call.err = ErrStaleResponse
		}
	case ok && version == e.version:
		// Unchanged data: extend the validity window, drop the duplicate.
		c.releaseLocked(value)
		e.softExp = now.Add(c.cfg.SoftTTL)
		e.hardExp = now.Add(c.cfg.HardTTL)
		e.fails = 0
		e.lastErr = nil
		call.res = Result{Value: e.value, Version: e.version, UsableUntil: e.hardExp}
	default:
		ne := &entry{
			value:   value,
			version: version,
			softExp: now.Add(c.cfg.SoftTTL),
			hardExp: now.Add(c.cfg.HardTTL),
		}
		if ok {
			c.releaseLocked(e.value)
		}
		c.entries[key] = ne
		call.res = Result{Value: value, Version: version, UsableUntil: ne.hardExp}
	}
	close(call.done)
}

// Invalidate deletes key and bumps its generation so that any refresh
// already in flight for the old generation is discarded on completion
// and cannot resurrect the key.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gens[key]++
	if e, ok := c.entries[key]; ok {
		c.releaseLocked(e.value)
		delete(c.entries, key)
	}
}

// Close releases every stored value, cancels in-flight source calls and
// blocks until all refresh goroutines have finished, so every borrowed
// resource is released before Close returns. Gets after Close fail with
// ErrClosed. Close is idempotent.
func (c *Cache) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		for key, e := range c.entries {
			c.releaseLocked(e.value)
			delete(c.entries, key)
		}
		c.cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Cache) releaseLocked(value interface{}) {
	if c.cfg.Release != nil && value != nil {
		c.cfg.Release(value)
	}
}
