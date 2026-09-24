package negcache

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed is returned by Get after Close.
var ErrClosed = errors.New("negcache: cache closed")

// Config wires the cache. Source, Now, Classify and Log are injectable
// functions; there is no remote cache dependency.
type Config struct {
	Source   Source
	Now      Clock
	Classify Classifier
	Log      Logger

	// OnStore is an optional hook invoked on every store/replace, used by
	// the independent Checker state machine. prev is nil on first insert.
	OnStore func(prev *EntrySnapshot, next EntrySnapshot)

	NotFoundTTL  time.Duration // TTL for negative (not-found) entries
	TempBaseTTL  time.Duration // base TTL for temporary errors (backs off)
	TempMaxTTL   time.Duration // cap for temporary-error backoff
	PermanentTTL time.Duration // TTL for permanent errors
	HardTTL      time.Duration // absolute max lifetime of any entry
	Capacity     int           // max entries; negative entries count too
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Classify == nil {
		c.Classify = DefaultClassifier
	}
	if c.Log == nil {
		c.Log = func(LogEntry) {}
	}
	if c.Capacity <= 0 {
		c.Capacity = 1024
	}
	if c.TempMaxTTL < c.TempBaseTTL {
		c.TempMaxTTL = c.TempBaseTTL
	}
}

// Result is the outcome of a Get. A negative result carries the error
// classification and the source version observed by the fill.
type Result struct {
	Value         []byte
	Negative      bool
	Class         ErrorClass
	SourceVersion uint64
	FromCache     bool
}

// EntrySnapshot is an immutable view of a stored entry, emitted to OnStore.
type EntrySnapshot struct {
	Tenant        string
	Key           string
	Success       bool
	Class         ErrorClass
	Value         []byte
	SourceVersion uint64
	Gen           uint64
	StoredAt      time.Time
	ExpiresAt     time.Time
	HardExpireAt  time.Time
}

type ckey struct{ tenant, key string }

type entry struct {
	snap EntrySnapshot
	elem *list.Element
}

// flight is one in-flight source call shared by all waiters (singleflight).
type flight struct {
	done    chan struct{}
	res     Result
	err     error
	gen     uint64
	waiters int
	cancel  context.CancelFunc
}

// Cache is a tenant-isolated cache with negative-result support, per-class
// TTLs, temporary-error backoff, a hard TTL, generation-based race
// resolution and singleflight fills.
type Cache struct {
	cfg Config

	mu        sync.Mutex
	entries   map[ckey]*entry
	lru       *list.List // of ckey; front = most recently used
	gens      map[ckey]uint64
	tempFails map[ckey]int
	flights   map[ckey]*flight
	closed    bool

	inflight sync.WaitGroup
}

func New(cfg Config) *Cache {
	cfg.defaults()
	return &Cache{
		cfg:       cfg,
		entries:   make(map[ckey]*entry),
		lru:       list.New(),
		gens:      make(map[ckey]uint64),
		tempFails: make(map[ckey]int),
		flights:   make(map[ckey]*flight),
	}
}

// Get returns the cached value/negative result or fills from the source.
// Concurrent Gets for the same tenant+key share one source call; one caller
// canceling never pollutes the result delivered to the other waiters.
func (c *Cache) Get(ctx context.Context, tenant, key string) (Result, error) {
	k := ckey{tenant, key}
	now := c.cfg.Now()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Result{}, ErrClosed
	}
	if e, ok := c.entries[k]; ok {
		if now.Before(e.snap.ExpiresAt) {
			c.lru.MoveToFront(e.elem)
			res := resultOf(e.snap, true)
			c.mu.Unlock()
			return res, nil
		}
		c.removeLocked(e) // expired (TTL or hard TTL)
	}
	if f, ok := c.flights[k]; ok {
		f.waiters++
		c.mu.Unlock()
		return c.wait(ctx, k, f)
	}
	fctx, cancel := context.WithCancel(context.Background())
	f := &flight{done: make(chan struct{}), gen: c.gens[k], waiters: 1, cancel: cancel}
	c.flights[k] = f
	c.inflight.Add(1)
	c.mu.Unlock()

	go c.fill(fctx, k, f)
	return c.wait(ctx, k, f)
}

func (c *Cache) wait(ctx context.Context, k ckey, f *flight) (Result, error) {
	select {
	case <-f.done:
		return f.res, f.err
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		last := f.waiters == 0
		if last {
			f.cancel() // nobody left: stop the source call
		}
		c.mu.Unlock()
		c.cfg.Log(LogEntry{
			Time: c.cfg.Now(), Tenant: k.tenant, Key: k.key,
			Class: ClassCanceled, Msg: "caller canceled while waiting",
			Cancellation: true, // never reported as a source error
		})
		return Result{}, ctx.Err()
	}
}

// fill performs the source call and applies the generation rule: the result
// is stored only if no invalidation happened while the call was in flight.
func (c *Cache) fill(ctx context.Context, k ckey, f *flight) {
	defer c.inflight.Done()
	val, ver, err := c.cfg.Source(ctx, k.tenant, k.key)
	class := ClassNone
	if err != nil {
		class = c.cfg.Classify(err)
	}
	now := c.cfg.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[k] == f { // may have been detached by Invalidate
		delete(c.flights, k)
	}
	defer close(f.done)

	if c.gens[k] != f.gen {
		// Stale fill: an Invalidate (or a newer success) bumped the
		// generation while we were in flight. Deliver to our waiters but
		// never store, so a late error cannot overwrite a newer value.
		c.deliver(f, val, ver, err, class)
		return
	}

	switch class {
	case ClassNone:
		c.gens[k]++ // real success overwrites any negative result
		delete(c.tempFails, k)
		c.storeLocked(k, EntrySnapshot{
			Tenant: k.tenant, Key: k.key, Success: true, Class: ClassNone,
			Value: val, SourceVersion: ver, Gen: c.gens[k],
		}, now, c.cfg.HardTTL)
		f.res = Result{Value: val, SourceVersion: ver}
	case ClassNotFound:
		delete(c.tempFails, k)
		c.storeNegativeLocked(k, ver, class, now, c.cfg.NotFoundTTL)
		f.res = Result{Negative: true, Class: class, SourceVersion: ver}
	case ClassTemporary:
		ttl := c.backoffLocked(k)
		c.storeNegativeLocked(k, ver, class, now, ttl)
		f.res = Result{Negative: true, Class: class, SourceVersion: ver}
	case ClassPermanent:
		delete(c.tempFails, k)
		c.storeNegativeLocked(k, ver, class, now, c.cfg.PermanentTTL)
		f.res = Result{Negative: true, Class: class, SourceVersion: ver}
	case ClassCanceled:
		// Cancellation is never shared-cached.
		c.cfg.Log(LogEntry{
			Time: now, Tenant: k.tenant, Key: k.key, Class: class,
			Msg: "source call canceled", Cancellation: true,
		})
		f.err = err
	}
	if class != ClassNone && class != ClassCanceled {
		c.cfg.Log(LogEntry{
			Time: now, Tenant: k.tenant, Key: k.key, Class: class,
			Msg: "source error cached as negative", SourceErr: true,
		})
	}
}

// deliver handles a stale fill: waiters still get an answer, nothing is stored.
func (c *Cache) deliver(f *flight, val []byte, ver uint64, err error, class ErrorClass) {
	if class == ClassNone {
		f.res = Result{Value: val, SourceVersion: ver}
		return
	}
	if class == ClassCanceled {
		f.err = err
		return
	}
	f.res = Result{Negative: true, Class: class, SourceVersion: ver}
}

// backoffLocked returns the TTL for the next temporary failure: base
// doubling per consecutive temporary failure, capped at TempMaxTTL.
func (c *Cache) backoffLocked(k ckey) time.Duration {
	n := c.tempFails[k]
	c.tempFails[k] = n + 1
	ttl := c.cfg.TempBaseTTL
	for i := 0; i < n && ttl < c.cfg.TempMaxTTL; i++ {
		ttl *= 2
	}
	if ttl > c.cfg.TempMaxTTL {
		ttl = c.cfg.TempMaxTTL
	}
	return ttl
}

// storeNegativeLocked stores a negative entry, but a late error with an
// older source version can never push out a newer cached success.
func (c *Cache) storeNegativeLocked(k ckey, ver uint64, class ErrorClass, now time.Time, ttl time.Duration) {
	if old, ok := c.entries[k]; ok && old.snap.Success && ver < old.snap.SourceVersion {
		return
	}
	c.storeLocked(k, EntrySnapshot{
		Tenant: k.tenant, Key: k.key, Success: false, Class: class,
		SourceVersion: ver, Gen: c.gens[k],
	}, now, ttl)
}

// storeLocked inserts or replaces an entry, enforcing capacity (negative
// entries count too) with LRU eviction, and emits the OnStore snapshot.
func (c *Cache) storeLocked(k ckey, snap EntrySnapshot, now time.Time, ttl time.Duration) {
	snap.StoredAt = now
	snap.HardExpireAt = now.Add(c.cfg.HardTTL)
	snap.ExpiresAt = now.Add(ttl)
	if snap.ExpiresAt.After(snap.HardExpireAt) {
		snap.ExpiresAt = snap.HardExpireAt
	}

	var prev *EntrySnapshot
	if old, ok := c.entries[k]; ok {
		p := old.snap
		prev = &p
		c.lru.MoveToFront(old.elem)
		old.snap = snap
	} else {
		for len(c.entries) >= c.cfg.Capacity {
			back := c.lru.Back()
			if back == nil {
				break
			}
			c.removeLocked(c.entries[back.Value.(ckey)])
		}
		e := &entry{snap: snap}
		e.elem = c.lru.PushFront(k)
		c.entries[k] = e
	}
	if c.cfg.OnStore != nil {
		c.cfg.OnStore(prev, snap)
	}
}

func (c *Cache) removeLocked(e *entry) {
	c.lru.Remove(e.elem)
	delete(c.entries, ckey{e.snap.Tenant, e.snap.Key})
}

// Invalidate bumps the key generation and drops any cached entry. In-flight
// fills started before this call become stale and will not be stored; this
// is the same generation rule that governs expiry and success-return races.
func (c *Cache) Invalidate(tenant, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := ckey{tenant, key}
	c.gens[k]++
	delete(c.tempFails, k)
	delete(c.flights, k) // detach stale in-flight fill; its waiters keep their pointer
	if e, ok := c.entries[k]; ok {
		c.removeLocked(e)
	}
}

// Len reports the number of cached entries (negative entries included).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close cancels all in-flight source calls and waits for them to finish,
// releasing all resources. No goroutine outlives Close.
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	for _, f := range c.flights {
		f.cancel()
	}
	c.mu.Unlock()
	c.inflight.Wait()
}

func resultOf(s EntrySnapshot, fromCache bool) Result {
	if s.Success {
		return Result{Value: s.Value, SourceVersion: s.SourceVersion, FromCache: fromCache}
	}
	return Result{Negative: true, Class: s.Class, SourceVersion: s.SourceVersion, FromCache: fromCache}
}
