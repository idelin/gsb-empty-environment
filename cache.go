// Package negcache is a single-node, stdlib-only cache that stores negative
// results (classified errors) alongside positive ones. Error classes have
// their own TTLs, real successes override negatives, cancellation is never
// cached or shared, and a per-key generation rule decides every race between
// expiry, invalidation and late source responses.
package negcache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// Response is what a source returns for one key. Version is the source
// version and is carried by both positive and negative results.
type Response[V any] struct {
	Value   V
	Version uint64
	Err     *SourceError
}

// SourceFunc is the injectable data source (and failure injector).
type SourceFunc[V any] func(ctx context.Context, key string) Response[V]

// Logger receives cache events. Cancellation is logged with its own event
// names and never reported as a source error.
type Logger interface {
	Logf(format string, args ...any)
}

type LoggerFunc func(format string, args ...any)

func (f LoggerFunc) Logf(format string, args ...any) { f(format, args...) }

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

// Config tunes a Cache. Zero fields get defaults.
type Config[V any] struct {
	Source   SourceFunc[V]
	Clock    Clock
	Logger   Logger
	Capacity int // negative entries count too

	PositiveTTL  time.Duration
	NotFoundTTL  time.Duration
	TransientTTL time.Duration // base; doubles per consecutive failure
	PermanentTTL time.Duration
	HardTTL      time.Duration // absolute cap on any entry's lifetime
	BackoffMax   time.Duration // cap for transient backoff
}

func (c *Config[V]) defaults() {
	if c.Clock == nil {
		c.Clock = RealClock{}
	}
	if c.Logger == nil {
		c.Logger = discardLogger{}
	}
	if c.Capacity <= 0 {
		c.Capacity = 1024
	}
	if c.PositiveTTL <= 0 {
		c.PositiveTTL = 5 * time.Minute
	}
	if c.NotFoundTTL <= 0 {
		c.NotFoundTTL = 30 * time.Second
	}
	if c.TransientTTL <= 0 {
		c.TransientTTL = time.Second
	}
	if c.PermanentTTL <= 0 {
		c.PermanentTTL = 10 * time.Minute
	}
	if c.HardTTL <= 0 {
		c.HardTTL = time.Hour
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
}

type tenantKey struct {
	tenant string
	key    string
}

type entry[V any] struct {
	tkey       tenantKey
	value      V
	serr       *SourceError // non-nil for negative entries
	version    uint64       // source version, carried by negatives too
	storedAt   time.Time
	softExpiry time.Time
	hardExpiry time.Time
}

func (e *entry[V]) negative() bool { return e.serr != nil }

type call[V any] struct {
	done    chan struct{}
	res     Response[V]
	waiters int
	gen     uint64 // generation at fetch start; stale calls are not joinable
}

// Cache is safe for concurrent use.
type Cache[V any] struct {
	cfg    Config[V]
	clock  Clock
	log    Logger
	source SourceFunc[V]

	mu       sync.Mutex
	entries  map[tenantKey]*list.Element
	lru      *list.List
	gen      map[tenantKey]uint64 // per-key generation: the single race arbiter
	failures map[tenantKey]int    // consecutive transient failures (backoff)
	inflight map[tenantKey]*call[V]
	wg       sync.WaitGroup
}

func New[V any](cfg Config[V]) *Cache[V] {
	if cfg.Source == nil {
		panic("negcache: Source is required")
	}
	cfg.defaults()
	return &Cache[V]{
		cfg:      cfg,
		clock:    cfg.Clock,
		log:      cfg.Logger,
		source:   cfg.Source,
		entries:  make(map[tenantKey]*list.Element),
		lru:      list.New(),
		gen:      make(map[tenantKey]uint64),
		failures: make(map[tenantKey]int),
		inflight: make(map[tenantKey]*call[V]),
	}
}

// Get returns the cached or freshly fetched value. Cached negatives are
// returned as *SourceError carrying their class and source version.
func (c *Cache[V]) Get(ctx context.Context, tenant, key string) (V, error) {
	tk := tenantKey{tenant: tenant, key: key}
	now := c.clock.Now()

	c.mu.Lock()
	if el, ok := c.entries[tk]; ok {
		ent := el.Value.(*entry[V])
		if c.validLocked(ent, now) {
			c.lru.MoveToFront(el)
			c.mu.Unlock()
			if ent.negative() {
				var zero V
				return zero, ent.serr
			}
			return ent.value, nil
		}
		// Expiry joins the same generation rule as invalidation.
		c.removeLocked(el)
	}
	startGen := c.gen[tk]
	if cl, ok := c.inflight[tk]; ok && cl.gen == startGen {
		cl.waiters++
		c.mu.Unlock()
		return c.wait(ctx, tk, cl)
	}
	cl := &call[V]{done: make(chan struct{}), waiters: 1, gen: startGen}
	c.inflight[tk] = cl
	c.wg.Add(1)
	go c.fetch(tk, cl, startGen)
	c.mu.Unlock()
	return c.wait(ctx, tk, cl)
}

func (c *Cache[V]) wait(ctx context.Context, tk tenantKey, cl *call[V]) (V, error) {
	var zero V
	select {
	case <-cl.done:
		if cl.res.Err != nil {
			return zero, cl.res.Err
		}
		return cl.res.Value, nil
	case <-ctx.Done():
		// One caller's cancellation never aborts the shared fetch and is
		// never written into the cache.
		c.mu.Lock()
		cl.waiters--
		c.mu.Unlock()
		c.log.Logf("caller_canceled tenant=%q key=%q err=%v", tk.tenant, tk.key, ctx.Err())
		return zero, Canceled(ctx.Err())
	}
}

// fetch runs the source on a detached context so individual callers cannot
// cancel the shared work, then commits under the generation rule.
func (c *Cache[V]) fetch(tk tenantKey, cl *call[V], startGen uint64) {
	defer c.wg.Done()
	res := c.source(context.Background(), tk.key)

	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.inflight[tk]; ok && cur == cl {
		delete(c.inflight, tk)
	}
	now := c.clock.Now()

	switch {
	case res.Err != nil && res.Err.Class == ClassCanceled:
		// Cancellation is never cached and never logged as a source error.
		c.log.Logf("fetch_canceled tenant=%q key=%q", tk.tenant, tk.key)
	case res.Err == nil:
		delete(c.failures, tk) // success resets transient backoff
		if cur := c.entryLocked(tk); cur == nil || cur.negative() || res.Version >= cur.version {
			c.storeLocked(tk, &entry[V]{
				value:   res.Value,
				version: res.Version,
			}, c.cfg.PositiveTTL)
			c.log.Logf("commit_success tenant=%q key=%q version=%d", tk.tenant, tk.key, res.Version)
		} else {
			c.log.Logf("drop_stale_success tenant=%q key=%q version=%d", tk.tenant, tk.key, res.Version)
		}
	default:
		class := res.Err.Class
		if class == ClassTransient {
			c.failures[tk]++
		}
		switch {
		case c.gen[tk] != startGen:
			// A success, invalidation or expiry committed while we were
			// in flight: this late error must not overwrite it.
			c.log.Logf("drop_stale_negative tenant=%q key=%q class=%s version=%d", tk.tenant, tk.key, class, res.Version)
		case c.entryLocked(tk) != nil && !c.entryLocked(tk).negative():
			c.log.Logf("drop_negative_over_success tenant=%q key=%q class=%s", tk.tenant, tk.key, class)
		default:
			c.storeLocked(tk, &entry[V]{
				serr:    res.Err,
				version: res.Version,
			}, c.negativeTTLLocked(tk, class))
			c.log.Logf("commit_negative tenant=%q key=%q class=%s version=%d", tk.tenant, tk.key, class, res.Version)
		}
	}

	// Waiters get the freshest committed state when their own fetch lost
	// the generation race.
	if ent := c.entryLocked(tk); ent != nil && c.validLocked(ent, now) {
		if ent.negative() {
			cl.res = Response[V]{Err: ent.serr, Version: ent.version}
		} else {
			cl.res = Response[V]{Value: ent.value, Version: ent.version}
		}
	} else {
		cl.res = res
	}
	c.cleanupLocked(tk)
	close(cl.done)
}

// Invalidate removes the entry and bumps the generation, so any in-flight
// negative for the key loses its race.
func (c *Cache[V]) Invalidate(tenant, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tk := tenantKey{tenant: tenant, key: key}
	delete(c.failures, tk) // explicit invalidation resets backoff
	if el, ok := c.entries[tk]; ok {
		c.removeLocked(el)
		return
	}
	c.gen[tk]++
	c.cleanupLocked(tk)
}

// Close waits for in-flight fetches to finish. No goroutine outlives Close.
func (c *Cache[V]) Close() { c.wg.Wait() }

// Info describes a stored entry for inspection and tests.
type Info struct {
	Negative   bool
	Class      Class
	Version    uint64
	SoftExpiry time.Time
	HardExpiry time.Time
}

func (c *Cache[V]) Inspect(tenant, key string) (Info, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[tenantKey{tenant: tenant, key: key}]
	if !ok {
		return Info{}, false
	}
	ent := el.Value.(*entry[V])
	info := Info{
		Negative:   ent.negative(),
		Version:    ent.version,
		SoftExpiry: ent.softExpiry,
		HardExpiry: ent.hardExpiry,
	}
	if ent.negative() {
		info.Class = ent.serr.Class
	}
	return info, true
}

// Stats exposes internal resource usage (for leak checks).
type Stats struct {
	Entries  int
	Inflight int
	Waiters  int
	GenKeys  int
}

func (c *Cache[V]) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Stats{
		Entries:  len(c.entries),
		Inflight: len(c.inflight),
		GenKeys:  len(c.gen),
	}
	for _, cl := range c.inflight {
		s.Waiters += cl.waiters
	}
	return s
}

func (c *Cache[V]) entryLocked(tk tenantKey) *entry[V] {
	if el, ok := c.entries[tk]; ok {
		return el.Value.(*entry[V])
	}
	return nil
}

func (c *Cache[V]) validLocked(ent *entry[V], now time.Time) bool {
	if !now.Before(ent.softExpiry) {
		return false
	}
	return ent.hardExpiry.IsZero() || now.Before(ent.hardExpiry)
}

func (c *Cache[V]) storeLocked(tk tenantKey, ent *entry[V], ttl time.Duration) {
	ent.tkey = tk
	ent.storedAt = c.clock.Now()
	ent.softExpiry = ent.storedAt.Add(ttl)
	if c.cfg.HardTTL > 0 {
		ent.hardExpiry = ent.storedAt.Add(c.cfg.HardTTL)
	}
	if el, ok := c.entries[tk]; ok {
		el.Value = ent
		c.lru.MoveToFront(el)
	} else {
		c.entries[tk] = c.lru.PushFront(ent)
	}
	c.gen[tk]++
	for len(c.entries) > c.cfg.Capacity {
		c.removeLocked(c.lru.Back())
	}
}

func (c *Cache[V]) removeLocked(el *list.Element) {
	ent := el.Value.(*entry[V])
	c.lru.Remove(el)
	delete(c.entries, ent.tkey)
	c.gen[ent.tkey]++
	c.cleanupLocked(ent.tkey)
}

// cleanupLocked releases per-key generation bookkeeping once nothing can
// observe it. The transient-failure counter intentionally survives expiry
// so backoff keeps growing across consecutive failures; it is reset only
// by a real success or an explicit Invalidate.
func (c *Cache[V]) cleanupLocked(tk tenantKey) {
	if _, ok := c.entries[tk]; ok {
		return
	}
	if _, ok := c.inflight[tk]; ok {
		return
	}
	delete(c.gen, tk)
}

// negativeTTLLocked picks the TTL for a negative entry. Transient errors
// back off exponentially with consecutive failures, capped at BackoffMax.
func (c *Cache[V]) negativeTTLLocked(tk tenantKey, class Class) time.Duration {
	switch class {
	case ClassNotFound:
		return c.cfg.NotFoundTTL
	case ClassPermanent:
		return c.cfg.PermanentTTL
	default: // transient
		d := c.cfg.TransientTTL
		for i := 1; i < c.failures[tk] && d < c.cfg.BackoffMax; i++ {
			d *= 2
			if d > c.cfg.BackoffMax {
				d = c.cfg.BackoffMax
			}
		}
		return d
	}
}
