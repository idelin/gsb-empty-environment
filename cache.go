package hexgrid

import "sync"

// Cache is a fixed-capacity, sharded, zero-allocation uint64->uint64 cache
// for hot paths (e.g. memoising quantised coordinate -> cell lookups).
//
//   - Fixed memory: 64 shards x 8 ways, no maps, no slices, no growth.
//   - Get/Put never allocate and never block on GC-sensitive structures.
//   - Eviction is per-shard oldest-first (approximate LRU).
//   - Key 0 is reserved (treated as "empty"); CacheKey never produces it.
//
// Safe for concurrent use.
const (
	cacheShards = 64
	cacheWays   = 8
)

type cacheShard struct {
	mu    sync.Mutex
	keys  [cacheWays]uint64
	vals  [cacheWays]uint64
	age   [cacheWays]uint32
	clock uint32
	used  int
}

// Cache is a sharded fixed-size zero-allocation cache.
type Cache struct {
	shards [cacheShards]cacheShard
}

// mix64 is the splitmix64 finaliser, used to spread keys across shards.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// Get looks up key. Never allocates.
func (c *Cache) Get(key uint64) (uint64, bool) {
	if key == 0 {
		return 0, false
	}
	sh := &c.shards[mix64(key)&(cacheShards-1)]
	sh.mu.Lock()
	for i := 0; i < cacheWays; i++ {
		if sh.keys[i] == key {
			sh.clock++
			sh.age[i] = sh.clock
			v := sh.vals[i]
			sh.mu.Unlock()
			return v, true
		}
	}
	sh.mu.Unlock()
	return 0, false
}

// Put inserts or updates key. Evicts the oldest entry in the shard when
// full. Never allocates.
func (c *Cache) Put(key, val uint64) {
	if key == 0 {
		return
	}
	sh := &c.shards[mix64(key)&(cacheShards-1)]
	sh.mu.Lock()
	empty := -1
	oldest := 0
	var minAge uint32 = ^uint32(0)
	for i := 0; i < cacheWays; i++ {
		if sh.keys[i] == key {
			sh.vals[i] = val
			sh.clock++
			sh.age[i] = sh.clock
			sh.mu.Unlock()
			return
		}
		if sh.keys[i] == 0 {
			if empty < 0 {
				empty = i
			}
		} else if sh.age[i] < minAge {
			minAge = sh.age[i]
			oldest = i
		}
	}
	slot := oldest
	if empty >= 0 {
		slot = empty
		sh.used++
	}
	sh.keys[slot] = key
	sh.vals[slot] = val
	sh.clock++
	sh.age[slot] = sh.clock
	sh.mu.Unlock()
}

// Len returns the number of entries currently stored.
func (c *Cache) Len() int {
	n := 0
	for i := 0; i < cacheShards; i++ {
		sh := &c.shards[i]
		sh.mu.Lock()
		n += sh.used
		sh.mu.Unlock()
	}
	return n
}

// Capacity returns the maximum number of entries (64 shards x 8 ways = 512).
func (c *Cache) Capacity() int { return cacheShards * cacheWays }

// CacheKey derives a non-zero cache key from a coordinate quantised to
// ~0.11 m (1e-6 degrees) plus the resolution. Never allocates.
func CacheKey(lat, lng float64, res int) uint64 {
	if lat > 90 {
		lat = 90
	} else if lat < -90 {
		lat = -90
	}
	if lng > 180 {
		lng = 180
	} else if lng < -180 {
		lng = -180
	}
	la := uint64(int64((lat + 90) * 1e6))  // 0..180,000,000 (28 bits)
	ln := uint64(int64((lng + 180) * 1e6)) // 0..360,000,000 (29 bits)
	return 1<<63 | la<<33 | ln<<4 | uint64(res&0xF)
}
