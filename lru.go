package paircache

import (
	"container/list"
	"sync"
	"time"
)

// LRU is a plain size-aware LRU used as the comparison baseline. It shares
// the lease, physical-accounting and injection machinery with TwoQ so Stats
// are directly comparable.
type LRU struct {
	mu    sync.Mutex
	cap   int64
	load  Loader
	now   func() time.Time
	acct  accounting
	lru   *list.List // front = MRU, *entry
	ix    map[string]*list.Element
	bytes int64
	stats Stats
}

// NewLRU builds an LRU cache with a fixed byte capacity. RecentRatio and
// GhostLimit in Options are ignored.
func NewLRU(opt Options) *LRU {
	opt = opt.withDefaults()
	return &LRU{
		cap:  opt.CapacityBytes,
		load: opt.Loader,
		now:  opt.Now,
		acct: accounting{onFree: opt.OnFree},
		lru:  list.New(),
		ix:   map[string]*list.Element{},
	}
}

// Acquire returns a lease for key, loading it on miss.
func (c *LRU) Acquire(key string) (*Lease, error) {
	c.mu.Lock()
	if el, ok := c.ix[key]; ok {
		c.lru.MoveToFront(el)
		e := el.Value.(*entry)
		e.at = c.now()
		e.refs++
		c.stats.Hits++
		l := c.leaseLocked(e)
		if e.val != nil {
			c.mu.Unlock()
			return l, nil
		}
		c.mu.Unlock()
		if err := c.coldLoad(e); err != nil {
			l.Release()
			return nil, err
		}
		return l, nil
	}
	c.stats.Misses++
	c.mu.Unlock()

	if c.load == nil {
		return nil, ErrNoLoader
	}
	val, err := c.load(key) // outside the lock: loader may be slow

	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Loads++
	if err != nil {
		c.stats.LoadErrors++
		return nil, err
	}
	if el, ok := c.ix[key]; ok { // inserted concurrently while loading
		c.lru.MoveToFront(el)
		e := el.Value.(*entry)
		e.refs++
		c.stats.Hits++
		c.stats.Misses--
		c.fillColdLocked(e, val)
		return c.leaseLocked(e), nil
	}
	e := &entry{key: key, val: val, size: int64(len(val)), at: c.now(), refs: 1}
	c.acct.alloc(e.size)
	if e.size > c.cap {
		e.evicted = true // too large to ever cache
		return c.leaseLocked(e), nil
	}
	c.ix[key] = c.lru.PushFront(e)
	c.bytes += e.size
	c.evictLocked()
	return c.leaseLocked(e), nil
}

// Add inserts or replaces a value without consulting the loader.
func (c *LRU) Add(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.ix[key]; ok {
		e := el.Value.(*entry)
		delta := int64(len(val)) - e.size
		e.val = val
		e.size = int64(len(val))
		c.bytes += delta
		c.acct.adjust(delta)
		c.lru.MoveToFront(el)
		c.evictLocked()
		return
	}
	e := &entry{key: key, val: val, size: int64(len(val)), at: c.now()}
	c.acct.alloc(e.size)
	if e.size > c.cap {
		e.evicted = true
		c.acct.free(e)
		return
	}
	c.ix[key] = c.lru.PushFront(e)
	c.bytes += e.size
	c.evictLocked()
}

// Delete removes key. Leased entries stay allocated until released.
func (c *LRU) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.ix[key]; ok {
		e := el.Value.(*entry)
		c.lru.Remove(el)
		delete(c.ix, key)
		c.bytes -= e.size
		e.evicted = true
		c.acct.free(e)
	}
}

// Stats returns a consistent snapshot of the counters.
func (c *LRU) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats
	s.ResidentEntries = int64(c.lru.Len())
	s.ResidentBytes = c.bytes
	s.PhysicalBytes = c.acct.phys
	s.PeakPhysicalBytes = c.acct.peak
	return s
}

func (c *LRU) leaseLocked(e *entry) *Lease {
	return &Lease{e: e, release: func() {
		c.mu.Lock()
		e.refs--
		c.acct.free(e)
		c.mu.Unlock()
	}}
}

func (c *LRU) coldLoad(e *entry) error {
	if c.load == nil {
		return ErrNoLoader
	}
	val, err := c.load(e.key)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Loads++
	if err != nil {
		c.stats.LoadErrors++
		return err
	}
	c.fillColdLocked(e, val)
	return nil
}

func (c *LRU) fillColdLocked(e *entry, val []byte) {
	if e.val != nil {
		return
	}
	delta := int64(len(val)) - e.size
	e.val = val
	e.size = int64(len(val))
	if _, ok := c.ix[e.key]; ok {
		c.bytes += delta
	}
	c.acct.adjust(delta)
}

func (c *LRU) evictLocked() {
	for c.bytes > c.cap && c.lru.Len() > 0 {
		el := c.lru.Back()
		e := el.Value.(*entry)
		c.lru.Remove(el)
		delete(c.ix, e.key)
		c.bytes -= e.size
		e.evicted = true
		c.stats.Evictions++
		c.acct.free(e)
	}
}
