// Package shardcache implements a sharded, budget-limited cache with
// miss coalescing, reservation-based accounting for in-flight loads, and
// consistent cross-shard statistics snapshots.
package shardcache

import (
	"container/list"
	"context"
	"errors"
	"hash/fnv"
	"sync"
)

var (
	// ErrInsufficientBudget is returned when the global budget cannot cover
	// a load reservation or a settled value, even after eviction.
	ErrInsufficientBudget = errors.New("shardcache: insufficient budget")
	// ErrCostTooLarge is returned when a single value's cost exceeds MaxCost.
	ErrCostTooLarge = errors.New("shardcache: value cost exceeds total capacity")
	// ErrLoadCanceled is delivered to waiters when the underlying load was
	// canceled because every waiter abandoned it.
	ErrLoadCanceled = errors.New("shardcache: load canceled")
	// ErrNoLoader is returned by GetOrLoad when no Loader is configured.
	ErrNoLoader = errors.New("shardcache: no loader configured")
)

// CostFunc charges an integer cost for a stored key/value pair.
type CostFunc func(key string, value []byte) int64

// Loader fetches a value for a key. The context is canceled when all
// waiters have abandoned the load.
type Loader func(ctx context.Context, key string) ([]byte, error)

type Options struct {
	Shards  int                    // number of shards; defaults to 16
	MaxCost int64                  // global budget shared by resident entries and load reservations
	Cost    CostFunc               // actual cost charged at publish; defaults to len(value)
	Reserve func(key string) int64 // budget reserved before a load starts; defaults to 1
	Loader  Loader
}

// Stats counters. A counter increments at the moment described; Resident
// and Reserved are gauges.
type Stats struct {
	Hits     int64 // a lookup finds a resident entry
	Misses   int64 // a lookup finds no resident entry (per caller, even when joining an in-flight load)
	Loads    int64 // a reservation succeeds and the loader goroutine is started
	Cancels  int64 // a waiting caller abandons an in-flight load via context cancellation
	Evicts   int64 // a resident entry is removed to free budget (overwrite/Delete do not count)
	Resident int64 // bytes currently charged to resident entries
	Reserved int64 // bytes currently reserved by in-flight loads
}

type entry struct {
	key     string
	value   []byte // immutable after store
	cost    int64
	version uint64
	lruElem *list.Element
}

type loadID struct {
	key     string
	version uint64
}

// call is one coalesced in-flight load for a (key, version) pair.
type call struct {
	key     string
	version uint64
	reserve int64
	cancel  context.CancelFunc
	doneCh  chan struct{}

	mu        sync.Mutex // guards waiters, cancelled, done
	waiters   int
	cancelled bool
	done      bool

	// written before doneCh is closed; read after it is closed.
	val []byte
	err error
}

type shard struct {
	mu       sync.Mutex
	entries  map[string]*entry
	lru      *list.List // front = most recently used; *entry
	loads    map[loadID]*call
	versions map[string]uint64 // tracked only while loads are in flight for the key
	loadRefs map[string]int    // number of in-flight calls per key
	stats    Stats
}

type Cache struct {
	shards  []*shard
	maxCost int64
	cost    CostFunc
	reserve func(key string) int64
	loader  Loader
	cursor  int // eviction round-robin cursor; only touched with all shards locked
}

func New(opts Options) *Cache {
	n := opts.Shards
	if n <= 0 {
		n = 16
	}
	c := &Cache{
		maxCost: opts.MaxCost,
		cost:    opts.Cost,
		reserve: opts.Reserve,
		loader:  opts.Loader,
	}
	if c.cost == nil {
		c.cost = func(_ string, v []byte) int64 { return int64(len(v)) }
	}
	if c.reserve == nil {
		c.reserve = func(string) int64 { return 1 }
	}
	c.shards = make([]*shard, n)
	for i := range c.shards {
		c.shards[i] = &shard{
			entries:  make(map[string]*entry),
			lru:      list.New(),
			loads:    make(map[loadID]*call),
			versions: make(map[string]uint64),
			loadRefs: make(map[string]int),
		}
	}
	return c
}

func (c *Cache) shardFor(key string) *shard {
	h := fnv.New32a()
	h.Write([]byte(key))
	return c.shards[int(h.Sum32())%len(c.shards)]
}

// lockAll locks every shard in index order. Budget decisions, eviction,
// settling loads, and Stats snapshots run under this full lock, which is
// what makes cross-shard accounting and snapshots consistent. Single-shard
// lookups never hold one shard lock while acquiring another, so the fixed
// order is deadlock-free.
func (c *Cache) lockAll() {
	for _, s := range c.shards {
		s.mu.Lock()
	}
}

func (c *Cache) unlockAll() {
	for i := len(c.shards) - 1; i >= 0; i-- {
		c.shards[i].mu.Unlock()
	}
}

// totalUsed requires all shards locked.
func (c *Cache) totalUsed() int64 {
	var total int64
	for _, s := range c.shards {
		total += s.stats.Resident + s.stats.Reserved
	}
	return total
}

// evict frees at least need bytes of resident cost, round-robin across
// shards. Only entries actually removed are charged back.
// Requires all shards locked.
func (c *Cache) evict(need int64) {
	if need <= 0 {
		return
	}
	freed := int64(0)
	n := len(c.shards)
	for freed < need {
		progress := false
		for i := 0; i < n && freed < need; i++ {
			s := c.shards[(c.cursor+i)%n]
			back := s.lru.Back()
			if back == nil {
				continue
			}
			e := back.Value.(*entry)
			s.lru.Remove(back)
			delete(s.entries, e.key)
			s.stats.Resident -= e.cost
			s.stats.Evicts++
			freed += e.cost
			progress = true
		}
		c.cursor++
		if !progress {
			return
		}
	}
}

// Get returns a copy of the stored value.
func (c *Cache) Get(key string) ([]byte, bool) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		s.stats.Misses++
		return nil, false
	}
	s.stats.Hits++
	s.lru.MoveToFront(e.lruElem)
	return append([]byte(nil), e.value...), true
}

// Set stores a copy of value, charging the global budget and evicting LRU
// entries if needed. Replacing an existing key charges only the delta.
func (c *Cache) Set(key string, value []byte) error {
	v := append([]byte(nil), value...)
	cost := c.cost(key, v)
	if cost > c.maxCost {
		return ErrCostTooLarge
	}
	c.lockAll()
	defer c.unlockAll()
	s := c.shardFor(key)
	if e, ok := s.entries[key]; ok {
		s.stats.Resident -= e.cost
		s.lru.Remove(e.lruElem)
		delete(s.entries, key)
	}
	if need := c.totalUsed() + cost - c.maxCost; need > 0 {
		c.evict(need)
	}
	if c.totalUsed()+cost > c.maxCost {
		return ErrInsufficientBudget
	}
	if s.loadRefs[key] > 0 {
		s.versions[key]++ // invalidate in-flight loads for this key
	}
	e := &entry{key: key, value: v, cost: cost, version: s.versions[key]}
	e.lruElem = s.lru.PushFront(e)
	s.entries[key] = e
	s.stats.Resident += cost
	return nil
}

// Delete removes a key, freeing its actual charged cost.
func (c *Cache) Delete(key string) bool {
	c.lockAll()
	defer c.unlockAll()
	s := c.shardFor(key)
	e, ok := s.entries[key]
	if !ok {
		return false
	}
	s.stats.Resident -= e.cost
	s.lru.Remove(e.lruElem)
	delete(s.entries, key)
	if s.loadRefs[key] > 0 {
		s.versions[key]++
	}
	return true
}

// GetOrLoad returns the resident value if present; otherwise it joins the
// in-flight load for the current (key, version) or starts a new one with a
// budget reservation. Each caller's context is independent: one caller
// canceling does not affect the others, and when the last waiter leaves the
// underlying load is canceled.
func (c *Cache) GetOrLoad(ctx context.Context, key string) ([]byte, error) {
	if c.loader == nil {
		return nil, ErrNoLoader
	}
	s := c.shardFor(key)

	s.mu.Lock()
	if e, ok := s.entries[key]; ok {
		s.stats.Hits++
		s.lru.MoveToFront(e.lruElem)
		v := append([]byte(nil), e.value...)
		s.mu.Unlock()
		return v, nil
	}
	s.stats.Misses++
	ver := s.versions[key]
	if cl, ok := s.loads[loadID{key: key, version: ver}]; ok {
		joinCall(cl)
		s.mu.Unlock()
		return c.waitCall(ctx, cl)
	}
	s.mu.Unlock()

	c.lockAll()
	if e, ok := s.entries[key]; ok { // lost the race; serve without double-counting stats
		s.lru.MoveToFront(e.lruElem)
		v := append([]byte(nil), e.value...)
		c.unlockAll()
		return v, nil
	}
	ver = s.versions[key]
	id := loadID{key: key, version: ver}
	if cl, ok := s.loads[id]; ok {
		joinCall(cl)
		c.unlockAll()
		return c.waitCall(ctx, cl)
	}
	reserve := c.reserve(key)
	if reserve < 0 {
		reserve = 0
	}
	if c.totalUsed()+reserve > c.maxCost {
		c.unlockAll()
		return nil, ErrInsufficientBudget
	}
	s.stats.Reserved += reserve
	s.stats.Loads++
	lctx, cancel := context.WithCancel(context.Background())
	cl := &call{
		key:     key,
		version: ver,
		reserve: reserve,
		cancel:  cancel,
		doneCh:  make(chan struct{}),
		waiters: 1,
	}
	s.loads[id] = cl
	s.loadRefs[key]++
	if _, ok := s.versions[key]; !ok {
		s.versions[key] = ver
	}
	c.unlockAll()

	go c.runLoad(lctx, cl)
	return c.waitCall(ctx, cl)
}

func joinCall(cl *call) {
	cl.mu.Lock()
	cl.waiters++
	cl.mu.Unlock()
}

func (c *Cache) waitCall(ctx context.Context, cl *call) ([]byte, error) {
	select {
	case <-cl.doneCh:
		if cl.err != nil {
			return nil, cl.err
		}
		return append([]byte(nil), cl.val...), nil
	case <-ctx.Done():
		cl.mu.Lock()
		cl.waiters--
		if cl.waiters == 0 && !cl.done {
			cl.cancelled = true
			cl.cancel()
		}
		cl.mu.Unlock()
		s := c.shardFor(cl.key)
		s.mu.Lock()
		s.stats.Cancels++
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Cache) runLoad(ctx context.Context, cl *call) {
	val, err := c.loader(ctx, cl.key)
	c.settle(cl, val, err)
}

// settle releases the reservation and either publishes the value at its
// true cost or frees everything on failure, cancellation, or overage.
func (c *Cache) settle(cl *call, val []byte, err error) {
	cl.mu.Lock()
	cancelled := cl.cancelled
	cl.mu.Unlock()

	c.lockAll()
	s := c.shardFor(cl.key)
	s.stats.Reserved -= cl.reserve

	if err == nil && cancelled {
		err = ErrLoadCanceled
	}
	if err == nil {
		cost := c.cost(cl.key, val)
		stale := true
		if cur, ok := s.versions[cl.key]; ok && cur == cl.version {
			if _, present := s.entries[cl.key]; !present {
				stale = false
			}
		}
		switch {
		case cost > c.maxCost:
			err = ErrCostTooLarge
		case stale:
			// A Set/Delete invalidated this load while it ran: drop the
			// stale result so it cannot overwrite the newer value. The
			// reservation is already released and nothing is charged.
		default:
			if need := c.totalUsed() + cost - c.maxCost; need > 0 {
				c.evict(need)
			}
			if c.totalUsed()+cost > c.maxCost {
				err = ErrInsufficientBudget
			} else {
				stored := append([]byte(nil), val...)
				e := &entry{key: cl.key, value: stored, cost: cost, version: cl.version}
				e.lruElem = s.lru.PushFront(e)
				s.entries[cl.key] = e
				s.stats.Resident += cost
			}
		}
	}

	delete(s.loads, loadID{key: cl.key, version: cl.version})
	s.loadRefs[cl.key]--
	if s.loadRefs[cl.key] == 0 {
		delete(s.versions, cl.key)
	}
	c.unlockAll()

	cl.mu.Lock()
	cl.done = true
	cl.val = val
	cl.err = err
	cl.mu.Unlock()
	close(cl.doneCh)
}

// Stats returns a consistent snapshot aggregated across all shards: every
// shard is locked before any is read.
func (c *Cache) Stats() Stats {
	c.lockAll()
	defer c.unlockAll()
	var out Stats
	for _, s := range c.shards {
		out.Hits += s.stats.Hits
		out.Misses += s.stats.Misses
		out.Loads += s.stats.Loads
		out.Cancels += s.stats.Cancels
		out.Evicts += s.stats.Evicts
		out.Resident += s.stats.Resident
		out.Reserved += s.stats.Reserved
	}
	return out
}
