package paircache

import (
	"container/list"
	"sync"
	"time"
)

// ghostRec is a key-only record of a recently evicted (or deleted) entry.
type ghostRec struct {
	key  string
	size int64
	at   time.Time
}

// TwoQ is a size-aware 2Q cache. First-touch entries live in a bounded FIFO
// (A1in); keys evicted from it leave key-only ghost records (A1out); a second
// touch promotes the key into the hot LRU (Am). Sequential scans flow through
// A1in only and therefore cannot displace Am, so a scan does not flush the
// hot set. This separation of first vs. repeat accesses is the whole point;
// on workloads with no reuse distinction (pure uniform random, tight loops
// just over capacity) 2Q is not better than plain LRU.
type TwoQ struct {
	mu        sync.Mutex
	cap       int64
	recentCap int64
	ghostCap  int
	load      Loader
	now       func() time.Time
	acct      accounting

	recent   *list.List // A1in, front = newest, *entry
	freq     *list.List // Am, front = MRU, *entry
	ghost    *list.List // A1out, front = newest, *ghostRec
	recentIx map[string]*list.Element
	freqIx   map[string]*list.Element
	ghostIx  map[string]*list.Element

	recentBytes int64
	freqBytes   int64

	stats Stats
}

// NewTwoQ builds a 2Q cache with a fixed byte capacity.
func NewTwoQ(opt Options) *TwoQ {
	opt = opt.withDefaults()
	recentCap := int64(float64(opt.CapacityBytes) * opt.RecentRatio)
	if recentCap < 1 {
		recentCap = 1
	}
	return &TwoQ{
		cap:       opt.CapacityBytes,
		recentCap: recentCap,
		ghostCap:  opt.GhostLimit,
		load:      opt.Loader,
		now:       opt.Now,
		acct:      accounting{onFree: opt.OnFree},
		recent:    list.New(),
		freq:      list.New(),
		ghost:     list.New(),
		recentIx:  map[string]*list.Element{},
		freqIx:    map[string]*list.Element{},
		ghostIx:   map[string]*list.Element{},
	}
}

// Acquire returns a lease for key, loading it on miss. The entry stays
// physically allocated until the lease is released, even if evicted.
func (c *TwoQ) Acquire(key string) (*Lease, error) {
	c.mu.Lock()
	if el, ok := c.freqIx[key]; ok {
		c.freq.MoveToFront(el)
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
	if el, ok := c.recentIx[key]; ok {
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
	// Re-check after the unlocked load: another goroutine may have inserted.
	if el, ok := c.freqIx[key]; ok {
		c.freq.MoveToFront(el)
		e := el.Value.(*entry)
		e.refs++
		c.stats.Hits++
		c.stats.Misses--
		c.fillColdLocked(e, val)
		return c.leaseLocked(e), nil
	}
	if el, ok := c.recentIx[key]; ok {
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
		// Larger than the whole cache: serve it but never cache it.
		e.evicted = true
		return c.leaseLocked(e), nil
	}
	if _, ok := c.ghostIx[key]; ok {
		// Second touch: promote into the hot queue.
		c.stats.GhostHits++
		c.removeGhostLocked(key)
		c.insertFreqLocked(e)
	} else {
		c.insertRecentLocked(e)
	}
	return c.leaseLocked(e), nil
}

// Add inserts or replaces a value without consulting the loader.
func (c *TwoQ) Add(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.freqIx[key]; ok {
		e := el.Value.(*entry)
		c.setValueLocked(e, val, &c.freqBytes)
		c.freq.MoveToFront(el)
		c.evictFreqLocked()
		return
	}
	if el, ok := c.recentIx[key]; ok {
		e := el.Value.(*entry)
		c.setValueLocked(e, val, &c.recentBytes)
		c.evictFreqLocked()
		return
	}
	e := &entry{key: key, val: val, size: int64(len(val)), at: c.now()}
	c.acct.alloc(e.size)
	if e.size > c.cap {
		e.evicted = true
		c.acct.free(e)
		return
	}
	if _, ok := c.ghostIx[key]; ok {
		c.removeGhostLocked(key)
		c.insertFreqLocked(e)
	} else {
		c.insertRecentLocked(e)
	}
}

// Delete removes key from the resident queues. A bounded ghost record is
// kept so the policy retains the key's history; ghost records are reclaimed
// FIFO once GhostLimit is exceeded, so deletes cannot grow metadata without
// bound. Leased entries stay allocated until their leases are released.
func (c *TwoQ) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	found := false
	var size int64
	if el, ok := c.freqIx[key]; ok {
		e := el.Value.(*entry)
		c.freq.Remove(el)
		delete(c.freqIx, key)
		c.freqBytes -= e.size
		e.evicted = true
		size = e.size
		c.acct.free(e)
		found = true
	}
	if el, ok := c.recentIx[key]; ok {
		e := el.Value.(*entry)
		c.recent.Remove(el)
		delete(c.recentIx, key)
		c.recentBytes -= e.size
		e.evicted = true
		size = e.size
		c.acct.free(e)
		found = true
	}
	if el, ok := c.ghostIx[key]; ok {
		size = el.Value.(*ghostRec).size
		found = true
	}
	if found {
		c.pushGhostLocked(key, size, c.now())
	}
}

// Stats returns a consistent snapshot of the counters.
func (c *TwoQ) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats
	s.ResidentEntries = int64(c.recent.Len() + c.freq.Len())
	s.GhostEntries = int64(c.ghost.Len())
	s.ResidentBytes = c.recentBytes + c.freqBytes
	s.PhysicalBytes = c.acct.phys
	s.PeakPhysicalBytes = c.acct.peak
	return s
}

func (c *TwoQ) leaseLocked(e *entry) *Lease {
	return &Lease{e: e, release: func() {
		c.mu.Lock()
		e.refs--
		c.acct.free(e)
		c.mu.Unlock()
	}}
}

// coldLoad fills the payload of a restored (metadata-only) entry. The policy
// decision (hit) was already made; only the value is missing.
func (c *TwoQ) coldLoad(e *entry) error {
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

func (c *TwoQ) fillColdLocked(e *entry, val []byte) {
	if e.val != nil {
		return
	}
	delta := int64(len(val)) - e.size
	e.val = val
	e.size = int64(len(val))
	if _, ok := c.freqIx[e.key]; ok {
		c.freqBytes += delta
	} else if _, ok := c.recentIx[e.key]; ok {
		c.recentBytes += delta
	}
	c.acct.adjust(delta)
}

func (c *TwoQ) setValueLocked(e *entry, val []byte, queueBytes *int64) {
	delta := int64(len(val)) - e.size
	e.val = val
	e.size = int64(len(val))
	*queueBytes += delta
	c.acct.adjust(delta)
}

func (c *TwoQ) insertRecentLocked(e *entry) {
	c.recentIx[e.key] = c.recent.PushFront(e)
	c.recentBytes += e.size
	for c.recentBytes > c.recentCap && c.recent.Len() > 0 {
		c.evictRecentTailLocked()
	}
	c.evictFreqLocked()
}

func (c *TwoQ) insertFreqLocked(e *entry) {
	c.freqIx[e.key] = c.freq.PushFront(e)
	c.freqBytes += e.size
	c.evictFreqLocked()
}

// evictFreqLocked keeps total resident bytes within capacity. Am evictions
// do not create ghost records (classic 2Q).
func (c *TwoQ) evictFreqLocked() {
	for c.recentBytes+c.freqBytes > c.cap && c.freq.Len() > 0 {
		el := c.freq.Back()
		e := el.Value.(*entry)
		c.freq.Remove(el)
		delete(c.freqIx, e.key)
		c.freqBytes -= e.size
		e.evicted = true
		c.stats.Evictions++
		c.acct.free(e)
	}
}

func (c *TwoQ) evictRecentTailLocked() {
	el := c.recent.Back()
	e := el.Value.(*entry)
	c.recent.Remove(el)
	delete(c.recentIx, e.key)
	c.recentBytes -= e.size
	e.evicted = true
	c.stats.Evictions++
	c.pushGhostLocked(e.key, e.size, e.at)
	c.acct.free(e)
}

func (c *TwoQ) pushGhostLocked(key string, size int64, at time.Time) {
	if el, ok := c.ghostIx[key]; ok {
		rec := el.Value.(*ghostRec)
		rec.size = size
		rec.at = at
		c.ghost.MoveToFront(el)
		return
	}
	c.ghostIx[key] = c.ghost.PushFront(&ghostRec{key: key, size: size, at: at})
	for c.ghost.Len() > c.ghostCap {
		tail := c.ghost.Back()
		c.ghost.Remove(tail)
		delete(c.ghostIx, tail.Value.(*ghostRec).key)
	}
}

func (c *TwoQ) removeGhostLocked(key string) {
	if el, ok := c.ghostIx[key]; ok {
		c.ghost.Remove(el)
		delete(c.ghostIx, key)
	}
}
