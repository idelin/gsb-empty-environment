package decayfreq

import (
	"container/list"
	"sync"
)

// CacheEntry is one admitted item.
type CacheEntry struct {
	Key  string
	Size int
}

// Cache is an LRU cache with a byte quota and a frequency-based admission
// policy: a candidate is admitted only if its sketch estimate is strictly
// greater than the estimate of the LRU victim it would replace. Frequent
// items are therefore more likely to be retained, but estimates are
// probabilistic upper bounds, not exact frequencies.
//
// Admission, eviction, and byte-quota accounting happen atomically under a
// single mutex: a rejected candidate leaves the cache completely unchanged.
type Cache struct {
	mu     sync.Mutex
	sketch *Sketch
	quota  int
	used   int
	ll     *list.List // front = most recently used, Value is CacheEntry
	items  map[string]*list.Element
}

// NewCache builds a cache with the given byte quota backed by the sketch.
func NewCache(sketch *Sketch, byteQuota int) *Cache {
	if byteQuota < 0 {
		byteQuota = 0
	}
	return &Cache{
		sketch: sketch,
		quota:  byteQuota,
		ll:     list.New(),
		items:  make(map[string]*list.Element),
	}
}

// Admit attempts to admit key of the given size in bytes. It returns true if
// the key is present after the call (already cached or newly admitted).
func (c *Cache) Admit(key string, size int) bool {
	if size < 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return true
	}
	if size > c.quota {
		return false
	}
	cand := c.sketch.Estimate(key)
	// Phase 1 (no mutation): walk victims from the LRU tail and decide.
	freed := 0
	var victims []*list.Element
	for back := c.ll.Back(); c.used-freed+size > c.quota; back = back.Prev() {
		if back == nil {
			return false
		}
		victim := back.Value.(CacheEntry)
		if cand <= c.sketch.Estimate(victim.Key) {
			return false // rejected: no state was mutated
		}
		victims = append(victims, back)
		freed += victim.Size
	}
	// Phase 2: commit evictions, quota accounting, and insertion atomically.
	for _, e := range victims {
		c.evictLocked(e)
	}
	c.insertLocked(CacheEntry{Key: key, Size: size})
	return true
}

func (c *Cache) evictLocked(e *list.Element) {
	ent := e.Value.(CacheEntry)
	c.ll.Remove(e)
	delete(c.items, ent.Key)
	c.used -= ent.Size
}

func (c *Cache) insertLocked(ent CacheEntry) {
	e := c.ll.PushFront(ent)
	c.items[ent.Key] = e
	c.used += ent.Size
}

// Used returns the currently consumed bytes. It never exceeds the quota.
func (c *Cache) Used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Contains reports whether key is cached.
func (c *Cache) Contains(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok
}

// Snapshot returns entries ordered from most to least recently used.
func (c *Cache) Snapshot() []CacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CacheEntry, 0, c.ll.Len())
	for e := c.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(CacheEntry))
	}
	return out
}

// restoreLocked rebuilds entries from a front-to-back snapshot.
func (c *Cache) restore(entries []CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element, len(entries))
	c.used = 0
	for i := len(entries) - 1; i >= 0; i-- {
		c.insertLocked(entries[i])
	}
}
