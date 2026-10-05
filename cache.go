// Package cache implements a sharded, budget-limited cache with
// single-flight loads and reservation-based accounting.
//
// Capacity is charged by an integer cost function. While a value is being
// loaded the cache holds a fixed reservation against the global budget;
// when the load completes the reservation is settled against the real cost
// (the difference is charged or refunded). Load failure, waiter
// cancellation and over-budget results all release the reservation.
//
// Concurrent misses for the same key and version merge into a single load.
// A waiter cancelling its own context does not cancel the other waiters;
// once every waiter has left, the underlying load's context is cancelled.
//
// A Set that races an in-flight load for the same key invalidates the
// load's result: the stale value is never published over the newer one.
//
// Statistics semantics:
//   Hits     - Get found a resident entry for the exact key+version.
//   Misses   - Get found no resident entry (includes joined and failed loads).
//   Loads    - loader invocations actually started (new flights).
//   Cancels  - waiters that abandoned a flight via context cancellation.
//   Evicts   - resident entries removed to free budget.
//   Resident - gauge: cost units currently held by stored entries.
//   Reserved - gauge: cost units currently reserved by in-flight loads.
// Stats() takes the accounting lock and every shard lock in a fixed order,
// so the aggregated snapshot is consistent.
package cache

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

var (
	// ErrTooLarge is returned when a value's cost exceeds the total budget.
	ErrTooLarge = errors.New("cache: value cost exceeds budget")
	// ErrQuota is returned when the budget cannot cover a load reservation
	// or a Set because in-flight reservations hold the remaining budget.
	ErrQuota = errors.New("cache: insufficient budget")
	// ErrNoLoader is returned by a missing Get when no loader is configured.
	ErrNoLoader = errors.New("cache: no loader configured")
)

// CostFunc bills a key/value pair in integer cost units.
type CostFunc func(key string, value []byte) int64

// Loader loads a value for a key at a version. The context is cancelled
// once the last waiter abandons the load.
type Loader func(ctx context.Context, key string, version uint64) ([]byte, error)

// Stats is a consistent snapshot of the aggregated cache counters.
type Stats struct {
	Hits     uint64
	Misses   uint64
	Loads    uint64
	Cancels  uint64
	Evicts   uint64
	Resident int64
	Reserved int64
}

type keyver struct {
	key string
	ver uint64
}

type entry struct {
	elem *list.Element
	val  []byte
	cost int64
}

// flight is one in-flight load shared by all waiters of a key+version.
type flight struct {
	key    string
	ver    uint64
	gen    uint64 // setGen[key] observed at creation
	done   chan struct{}
	cancel context.CancelFunc

	waiters int // guarded by the owning shard's mutex

	// written exactly once before done is closed
	val []byte
	err error
}

type shard struct {
	mu      sync.Mutex
	entries map[keyver]*entry
	lru     *list.List // front is most recently used; Value is keyver
	flights map[keyver]*flight
	setGen  map[string]uint64 // bumped on every successful Set of the key

	hits    uint64
	misses  uint64
	loads   uint64
	cancels uint64
	evicts  uint64
}

// Cache is a sharded cache with a global cost budget.
//
// Lock order is always acc -> shard.mu; no path takes acc while holding a
// shard mutex, which keeps eviction and Stats deadlock-free.
type Cache struct {
	shards  []*shard
	mask    uint64
	cost    CostFunc
	loader  Loader
	reserve int64

	acc      sync.Mutex
	budget   int64
	used     int64 // resident + reserved
	resident int64
	reserved int64
	rr       int // round-robin eviction cursor
}

// Option configures a Cache.
type Option func(*Cache)

// WithShards sets the shard count (rounded up to a power of two).
func WithShards(n int) Option {
	return func(c *Cache) {
		p := 1
		for p < n {
			p <<= 1
		}
		c.mask = uint64(p - 1)
	}
}

// WithCostFunc sets the cost function; the default bills len(value).
func WithCostFunc(f CostFunc) Option {
	return func(c *Cache) { c.cost = f }
}

// WithLoader sets the loader used on misses.
func WithLoader(l Loader) Option {
	return func(c *Cache) { c.loader = l }
}

// WithLoadReservation sets the budget units reserved per in-flight load.
func WithLoadReservation(n int64) Option {
	return func(c *Cache) {
		if n > 0 {
			c.reserve = n
		}
	}
}

// New creates a cache with the given total budget in cost units.
func New(budget int64, opts ...Option) *Cache {
	if budget <= 0 {
		panic("cache: budget must be positive")
	}
	c := &Cache{
		mask:   15,
		budget: budget,
		cost:   func(_ string, v []byte) int64 { return int64(len(v)) },
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.reserve <= 0 {
		c.reserve = c.budget / 8
		if c.reserve < 1 {
			c.reserve = 1
		}
	}
	n := int(c.mask) + 1
	c.shards = make([]*shard, n)
	for i := range c.shards {
		c.shards[i] = &shard{
			entries: make(map[keyver]*entry),
			lru:     list.New(),
			flights: make(map[keyver]*flight),
			setGen:  make(map[string]uint64),
		}
	}
	return c
}

func hash(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func (c *Cache) shardFor(key string) *shard {
	return c.shards[hash(key)&c.mask]
}

// Get returns a copy of the value for key at version, loading it on a miss.
func (c *Cache) Get(ctx context.Context, key string, version uint64) ([]byte, error) {
	kv := keyver{key: key, ver: version}
	sh := c.shardFor(key)

	sh.mu.Lock()
	if e, ok := sh.entries[kv]; ok {
		sh.lru.MoveToFront(e.elem)
		sh.hits++
		out := append([]byte(nil), e.val...)
		sh.mu.Unlock()
		return out, nil
	}
	sh.misses++
	if f, ok := sh.flights[kv]; ok {
		f.waiters++
		sh.mu.Unlock()
		return c.waitFlight(ctx, sh, f)
	}
	if c.loader == nil {
		sh.mu.Unlock()
		return nil, ErrNoLoader
	}
	fctx, cancel := context.WithCancel(context.Background())
	f := &flight{
		key:     key,
		ver:     version,
		gen:     sh.setGen[key],
		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	sh.flights[kv] = f
	sh.mu.Unlock()

	// Reserve budget before loading.
	c.acc.Lock()
	if c.used+c.reserve > c.budget {
		c.acc.Unlock()
		sh.mu.Lock()
		if sh.flights[kv] == f {
			delete(sh.flights, kv)
		}
		f.err = ErrQuota
		close(f.done)
		sh.mu.Unlock()
		return nil, ErrQuota
	}
	c.reserved += c.reserve
	c.used += c.reserve
	c.acc.Unlock()

	sh.mu.Lock()
	sh.loads++
	sh.mu.Unlock()

	go c.runLoad(fctx, f)
	return c.waitFlight(ctx, sh, f)
}

func (c *Cache) waitFlight(ctx context.Context, sh *shard, f *flight) ([]byte, error) {
	select {
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		return append([]byte(nil), f.val...), nil
	case <-ctx.Done():
		sh.mu.Lock()
		f.waiters--
		sh.cancels++
		last := f.waiters == 0
		sh.mu.Unlock()
		if last {
			f.cancel()
		}
		return nil, ctx.Err()
	}
}

// runLoad executes the loader and settles the reservation.
func (c *Cache) runLoad(ctx context.Context, f *flight) {
	val, err := c.loader(ctx, f.key, f.ver)
	kv := keyver{key: f.key, ver: f.ver}
	sh := c.shardFor(f.key)
	if err != nil {
		c.releaseReservation()
		sh.mu.Lock()
		if sh.flights[kv] == f {
			delete(sh.flights, kv)
		}
		f.err = err
		close(f.done)
		sh.mu.Unlock()
		return
	}

	cost := c.cost(f.key, val)
	delta := cost - c.reserve

	c.acc.Lock()
	c.evictLocked(delta)
	if c.used+delta > c.budget {
		// The real cost does not fit even after evicting everything.
		c.reserved -= c.reserve
		c.used -= c.reserve
		c.acc.Unlock()
		sh.mu.Lock()
		if sh.flights[kv] == f {
			delete(sh.flights, kv)
		}
		f.err = ErrTooLarge
		close(f.done)
		sh.mu.Unlock()
		return
	}
	c.reserved -= c.reserve
	c.resident += cost
	c.used += delta
	c.acc.Unlock()

	stored := append([]byte(nil), val...)

	sh.mu.Lock()
	if sh.flights[kv] == f {
		delete(sh.flights, kv)
	}
	if sh.setGen[f.key] != f.gen {
		// A Set raced this load: the result is stale, never publish it.
		sh.mu.Unlock()
		c.acc.Lock()
		c.resident -= cost
		c.used -= cost
		c.acc.Unlock()
		f.val = stored // waiters still receive what was loaded
		close(f.done)
		return
	}
	var oldCost int64
	if old, ok := sh.entries[kv]; ok {
		oldCost = old.cost
		sh.lru.Remove(old.elem)
		delete(sh.entries, kv)
	}
	e := &entry{val: stored, cost: cost}
	e.elem = sh.lru.PushFront(kv)
	sh.entries[kv] = e
	f.val = stored
	close(f.done)
	sh.mu.Unlock()

	if oldCost > 0 {
		c.acc.Lock()
		c.resident -= oldCost
		c.used -= oldCost
		c.acc.Unlock()
	}
}

func (c *Cache) releaseReservation() {
	c.acc.Lock()
	c.reserved -= c.reserve
	c.used -= c.reserve
	c.acc.Unlock()
}

// Set stores a copy of value for key at version, subject to the global
// budget. It invalidates any in-flight load for the same key.
func (c *Cache) Set(key string, version uint64, value []byte) error {
	stored := append([]byte(nil), value...)
	cost := c.cost(key, stored)
	if cost > c.budget {
		return ErrTooLarge
	}
	kv := keyver{key: key, ver: version}
	sh := c.shardFor(key)

	c.acc.Lock()
	c.evictLocked(cost)
	if c.used+cost > c.budget {
		c.acc.Unlock()
		return ErrQuota
	}
	c.resident += cost
	c.used += cost
	c.acc.Unlock()

	sh.mu.Lock()
	sh.setGen[key]++
	var oldCost int64
	if old, ok := sh.entries[kv]; ok {
		oldCost = old.cost
		sh.lru.Remove(old.elem)
		delete(sh.entries, kv)
	}
	e := &entry{val: stored, cost: cost}
	e.elem = sh.lru.PushFront(kv)
	sh.entries[kv] = e
	sh.mu.Unlock()

	if oldCost > 0 {
		c.acc.Lock()
		c.resident -= oldCost
		c.used -= oldCost
		c.acc.Unlock()
	}
	return nil
}

// evictLocked evicts LRU entries until need more units fit or the cache is
// empty. Only actually removed entries release budget. c.acc must be held.
func (c *Cache) evictLocked(need int64) {
	for c.used+need > c.budget {
		if !c.evictOneLocked() {
			return
		}
	}
}

func (c *Cache) evictOneLocked() bool {
	n := len(c.shards)
	for i := 0; i < n; i++ {
		c.rr = (c.rr + 1) % n
		sh := c.shards[c.rr]
		sh.mu.Lock()
		back := sh.lru.Back()
		if back == nil {
			sh.mu.Unlock()
			continue
		}
		kv := back.Value.(keyver)
		e := sh.entries[kv]
		delete(sh.entries, kv)
		sh.lru.Remove(back)
		sh.evicts++
		sh.mu.Unlock()
		c.used -= e.cost
		c.resident -= e.cost
		return true
	}
	return false
}

// Stats returns a consistent snapshot aggregated over all shards.
func (c *Cache) Stats() Stats {
	c.acc.Lock()
	for _, sh := range c.shards {
		sh.mu.Lock()
	}
	s := Stats{
		Resident: c.resident,
		Reserved: c.reserved,
	}
	for _, sh := range c.shards {
		s.Hits += sh.hits
		s.Misses += sh.misses
		s.Loads += sh.loads
		s.Cancels += sh.cancels
		s.Evicts += sh.evicts
	}
	for _, sh := range c.shards {
		sh.mu.Unlock()
	}
	c.acc.Unlock()
	return s
}
