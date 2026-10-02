package zerocache

import "sync"

// shardCount is the number of cache shards; keys are striped across shards
// to reduce lock contention.
const shardCount = 64

// cacheEntry is a single cached cell list backed by a pooled buffer.
type cacheEntry struct {
	cells []uint64
}

type shard struct {
	mu       sync.RWMutex
	items    map[uint64]cacheEntry
	fifo     []uint64 // insertion-order key ring for FIFO eviction
	head     int      // ring index of the oldest slot
	slots    int      // number of occupied ring slots (may include stale keys)
	perShard int
}

// Cache is a sharded, fixed-capacity uint64 -> []uint64 cache. Value
// storage comes from a SlicePool, so lookups and insertions on the hot
// path perform no heap allocations. Eviction is FIFO per shard. Cache is
// safe for concurrent use.
type Cache struct {
	pool   *SlicePool
	shards [shardCount]shard
}

// NewCache creates a cache whose values are backed by pool. perShard is the
// maximum number of entries per shard; the pool must have capacity for at
// least shardCount*perShard buffers so that every stored entry owns one.
func NewCache(pool *SlicePool, perShard int) *Cache {
	if perShard <= 0 {
		panic("zerocache: invalid shard capacity")
	}
	c := &Cache{pool: pool}
	for i := range c.shards {
		c.shards[i].items = make(map[uint64]cacheEntry, perShard)
		c.shards[i].fifo = make([]uint64, perShard)
		c.shards[i].perShard = perShard
	}
	return c
}

func (c *Cache) shardOf(key uint64) *shard {
	// Fibonacci hashing to spread sequential cell ids across shards.
	h := key * 11400714819323198485
	return &c.shards[h>>58]
}

// Get copies the cached value for key into dst and returns the number of
// elements copied. It returns -1 on a miss or when dst is too small.
// Get performs no heap allocations.
func (c *Cache) Get(key uint64, dst []uint64) int {
	s := c.shardOf(key)
	s.mu.RLock()
	e, ok := s.items[key]
	if !ok || len(e.cells) > len(dst) {
		s.mu.RUnlock()
		return -1
	}
	n := copy(dst, e.cells)
	s.mu.RUnlock()
	return n
}

// Len returns the length of the cached value for key, or -1 on a miss.
func (c *Cache) Len(key uint64) int {
	s := c.shardOf(key)
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		return -1
	}
	return len(e.cells)
}

// Put stores a copy of cells under key, evicting the oldest entry in the
// shard if necessary. It returns false if the value cannot be stored
// (empty value, or the backing pool is exhausted).
func (c *Cache) Put(key uint64, cells []uint64) bool {
	if len(cells) == 0 || len(cells) > c.pool.BufCap() {
		return false
	}
	s := c.shardOf(key)
	s.mu.Lock()
	if e, ok := s.items[key]; ok {
		// Overwrite in place; all pooled buffers share one capacity.
		e.cells = e.cells[:len(cells)]
		copy(e.cells, cells)
		s.items[key] = e
		s.mu.Unlock()
		return true
	}
	for s.slots == s.perShard {
		// Evict the oldest slot and recycle its buffer if still live.
		oldest := s.fifo[s.head]
		s.head = (s.head + 1) % s.perShard
		s.slots--
		if e, ok := s.items[oldest]; ok {
			delete(s.items, oldest)
			s.mu.Unlock()
			c.pool.Put(e.cells)
			s.mu.Lock()
		}
	}
	// The eviction loop dropped the lock; another goroutine may have
	// inserted the same key meanwhile. Re-check before inserting to avoid
	// clobbering that entry (its buffer would leak).
	if e, ok := s.items[key]; ok {
		e.cells = e.cells[:len(cells)]
		copy(e.cells, cells)
		s.items[key] = e
		s.mu.Unlock()
		return true
	}
	buf := c.pool.Get()
	if buf == nil {
		s.mu.Unlock()
		return false
	}
	buf = buf[:len(cells)]
	copy(buf, cells)
	s.items[key] = cacheEntry{cells: buf}
	s.fifo[(s.head+s.slots)%s.perShard] = key
	s.slots++
	s.mu.Unlock()
	return true
}

// Delete removes key from the cache, returning its buffer to the pool.
func (c *Cache) Delete(key uint64) {
	s := c.shardOf(key)
	s.mu.Lock()
	e, ok := s.items[key]
	if ok {
		delete(s.items, key)
	}
	s.mu.Unlock()
	if ok {
		c.pool.Put(e.cells)
	}
}

// Close returns every cached buffer to the pool and resets the cache.
// After Close, the cache may be reused.
func (c *Cache) Close() {
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		entries := make([]cacheEntry, 0, len(s.items))
		for key, e := range s.items {
			entries = append(entries, e)
			delete(s.items, key)
		}
		s.head = 0
		s.slots = 0
		s.mu.Unlock()
		for _, e := range entries {
			c.pool.Put(e.cells)
		}
	}
}
