package prefetch

import (
	"sync"
	"time"
)

// Value is a loaded payload tagged with a monotonic source version.
type Value struct {
	Data    any
	Version uint64
}

type cacheEntry struct {
	val Value
	at  time.Time
}

// Cache is a segmented, version-checked cache.
//
// The foreground segment (fgCap) holds values real requests loaded or hit;
// the prefetch segment (pfCap) holds speculative values. Prefetch entries
// can never evict foreground entries, so mispredictions cannot pollute the
// whole capacity. Prefetch entries may carry a TTL evaluated against the
// injected Clock.
//
// Invalidate records a version watermark; Publish rejects any value older
// than the watermark, so stale prefetch results cannot resurrect
// invalidated values.
type Cache struct {
	mu     sync.Mutex
	clock  Clock
	pfTTL  time.Duration
	fgCap  int
	pfCap  int
	fg     map[string]cacheEntry
	pf     map[string]cacheEntry
	fgLRU  []string
	pfLRU  []string
	minVer map[string]uint64
}

func NewCache(clock Clock, fgCap, pfCap int, pfTTL time.Duration) *Cache {
	return &Cache{
		clock:  clock,
		pfTTL:  pfTTL,
		fgCap:  fgCap,
		pfCap:  pfCap,
		fg:     make(map[string]cacheEntry),
		pf:     make(map[string]cacheEntry),
		minVer: make(map[string]uint64),
	}
}

// Get returns a cached value. A hit in the prefetch segment is promoted
// into the foreground segment.
func (c *Cache) Get(key string) (Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.fg[key]; ok {
		c.touch(&c.fgLRU, key)
		return e.val, true
	}
	if e, ok := c.pf[key]; ok {
		if c.pfTTL > 0 && c.clock.Now().Sub(e.at) > c.pfTTL {
			c.removePF(key)
			return Value{}, false
		}
		c.removePF(key)
		c.putFG(key, e.val)
		return e.val, true
	}
	return Value{}, false
}

// Publish stores v if it passes the version check. It returns false when
// the value was rejected (stale vs. invalidation watermark or vs. an
// existing newer/equal entry).
func (c *Cache) Publish(key string, v Value, prefetch bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v.Version < c.minVer[key] {
		return false
	}
	if e, ok := c.fg[key]; ok && e.val.Version >= v.Version {
		return false
	}
	if prefetch {
		if e, ok := c.pf[key]; ok && e.val.Version >= v.Version {
			return false
		}
		c.putPF(key, v)
		return true
	}
	delete(c.pf, key)
	c.removeKey(&c.pfLRU, key)
	c.putFG(key, v)
	return true
}

// Invalidate drops entries for key whose version is <= version and raises
// the watermark so older in-flight results are rejected on publish.
func (c *Cache) Invalidate(key string, version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.minVer[key] < version+1 {
		c.minVer[key] = version + 1
	}
	if e, ok := c.fg[key]; ok && e.val.Version <= version {
		delete(c.fg, key)
		c.removeKey(&c.fgLRU, key)
	}
	if e, ok := c.pf[key]; ok && e.val.Version <= version {
		c.removePF(key)
	}
}

// Len reports current segment occupancies (foreground, prefetch).
func (c *Cache) Len() (fg, pf int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.fg), len(c.pf)
}

func (c *Cache) putFG(key string, v Value) {
	if _, ok := c.fg[key]; !ok && len(c.fg) >= c.fgCap && len(c.fgLRU) > 0 {
		victim := c.fgLRU[0]
		c.fgLRU = c.fgLRU[1:]
		delete(c.fg, victim)
	}
	if _, ok := c.fg[key]; !ok {
		c.fgLRU = append(c.fgLRU, key)
	} else {
		c.touch(&c.fgLRU, key)
	}
	c.fg[key] = cacheEntry{val: v, at: c.clock.Now()}
}

func (c *Cache) putPF(key string, v Value) {
	if _, ok := c.pf[key]; !ok && len(c.pf) >= c.pfCap && len(c.pfLRU) > 0 {
		victim := c.pfLRU[0]
		c.pfLRU = c.pfLRU[1:]
		delete(c.pf, victim)
	}
	if _, ok := c.pf[key]; !ok {
		c.pfLRU = append(c.pfLRU, key)
	}
	c.pf[key] = cacheEntry{val: v, at: c.clock.Now()}
}

func (c *Cache) removePF(key string) {
	delete(c.pf, key)
	c.removeKey(&c.pfLRU, key)
}

func (c *Cache) touch(lru *[]string, key string) {
	c.removeKey(lru, key)
	*lru = append(*lru, key)
}

func (c *Cache) removeKey(lru *[]string, key string) {
	s := *lru
	for i, k := range s {
		if k == key {
			*lru = append(s[:i], s[i+1:]...)
			return
		}
	}
}
