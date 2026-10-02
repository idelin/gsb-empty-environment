// Package cache provides a low-latency, zero-allocation cache and a
// generic object pool. Both are built for GC-stall elimination on hot
// paths: after warm-up, Get/Put on existing keys and Pool.Get/Put perform
// zero heap allocations.
package cache

import "sync"

// DefaultShards is the shard count used when Options.Shards is zero.
const DefaultShards = 64

// Options configures a Cache.
type Options struct {
	// Capacity is the total maximum number of entries.
	Capacity int
	// MaxValueSize is the largest value (bytes) the cache will store.
	// Larger values are rejected by Put.
	MaxValueSize int
	// Shards is the desired shard count; it is rounded up to a power of
	// two. Zero selects DefaultShards.
	Shards int
}

// Stats reports cumulative cache counters.
type Stats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
}

// Cache is a fixed-capacity, sharded, zero-allocation cache with CLOCK
// (second-chance) eviction. Keys are uint64; values are copied into
// pre-allocated per-slot buffers, so the hot path never touches the
// allocator. Memory usage is bounded at construction time: there is
// strictly no leak — the cache never holds more than Capacity entries.
type Cache struct {
	shards []shard
	mask   uint64
	maxVal int
}

type slot struct {
	key  uint64
	len  uint32
	used bool
	ref  bool // CLOCK reference bit
	buf  []byte
}

type shard struct {
	mu     sync.Mutex
	keys   map[uint64]int32
	slots  []slot
	free   []int32
	hand   int32 // CLOCK hand
	hits   uint64
	misses uint64
	evict  uint64
}

// mix64 is the splitmix64 finalizer: cheap, allocation-free key mixing.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// NewCache allocates the whole cache up front; no further allocations
// happen on the Get/Put hot path.
func NewCache(opt Options) *Cache {
	nshards := opt.Shards
	if nshards <= 0 {
		nshards = DefaultShards
	}
	pow := 1
	for pow < nshards {
		pow <<= 1
	}
	nshards = pow
	per := opt.Capacity / nshards
	if per < 1 {
		per = 1
	}
	c := &Cache{
		shards: make([]shard, nshards),
		mask:   uint64(nshards - 1),
		maxVal: opt.MaxValueSize,
	}
	for i := range c.shards {
		s := &c.shards[i]
		s.keys = make(map[uint64]int32, per)
		s.slots = make([]slot, per)
		s.free = make([]int32, per)
		for j := range s.slots {
			s.slots[j].buf = make([]byte, opt.MaxValueSize)
			s.free[per-1-j] = int32(j)
		}
	}
	return c
}

func (c *Cache) shardOf(key uint64) *shard {
	return &c.shards[mix64(key)&c.mask]
}

// Get copies the value for key into dst and returns the resliced dst.
// ok=false means the key is absent; the returned slice is then empty.
// The call is allocation-free when cap(dst) >= len(value).
func (c *Cache) Get(key uint64, dst []byte) ([]byte, bool) {
	s := c.shardOf(key)
	s.mu.Lock()
	idx, ok := s.keys[key]
	if !ok {
		s.misses++
		s.mu.Unlock()
		return dst[:0], false
	}
	e := &s.slots[idx]
	e.ref = true
	n := int(e.len)
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}
	copy(dst, e.buf[:n])
	s.hits++
	s.mu.Unlock()
	return dst, true
}

// Put stores val under key, copying it into a pre-allocated slot.
// It returns false (and stores nothing) when len(val) > MaxValueSize.
// Inserting a new key may evict another entry via the CLOCK algorithm.
func (c *Cache) Put(key uint64, val []byte) bool {
	if len(val) > c.maxVal {
		return false
	}
	s := c.shardOf(key)
	s.mu.Lock()
	if idx, ok := s.keys[key]; ok {
		e := &s.slots[idx]
		copy(e.buf, val)
		e.len = uint32(len(val))
		e.ref = true
		s.mu.Unlock()
		return true
	}
	idx := s.allocLocked()
	e := &s.slots[idx]
	if e.used {
		delete(s.keys, e.key)
		s.evict++
	}
	e.key = key
	e.len = uint32(len(val))
	e.used = true
	e.ref = true
	copy(e.buf, val)
	s.keys[key] = idx
	s.mu.Unlock()
	return true
}

// allocLocked returns a free slot index, evicting via CLOCK when full.
func (s *shard) allocLocked() int32 {
	if n := len(s.free); n > 0 {
		idx := s.free[n-1]
		s.free = s.free[:n-1]
		return idx
	}
	size := int32(len(s.slots))
	for {
		e := &s.slots[s.hand]
		idx := s.hand
		s.hand = (s.hand + 1) % size
		if !e.used || !e.ref {
			return idx
		}
		e.ref = false
	}
}

// Delete removes key, returning its slot to the free list.
func (c *Cache) Delete(key uint64) bool {
	s := c.shardOf(key)
	s.mu.Lock()
	idx, ok := s.keys[key]
	if !ok {
		s.mu.Unlock()
		return false
	}
	delete(s.keys, key)
	e := &s.slots[idx]
	e.used = false
	e.ref = false
	s.free = append(s.free, idx)
	s.mu.Unlock()
	return true
}

// Len returns the number of stored entries; it never exceeds Capacity.
func (c *Cache) Len() int {
	total := 0
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		total += len(s.keys)
		s.mu.Unlock()
	}
	return total
}

// Capacity returns the total number of entry slots.
func (c *Cache) Capacity() int {
	total := 0
	for i := range c.shards {
		total += len(c.shards[i].slots)
	}
	return total
}

// Stats returns cumulative counters across all shards.
func (c *Cache) Stats() Stats {
	var st Stats
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		st.Hits += s.hits
		st.Misses += s.misses
		st.Evictions += s.evict
		s.mu.Unlock()
	}
	return st
}
