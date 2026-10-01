// Package cache implements a low-latency, sharded, fixed-capacity byte
// cache. Values live in pooled buffers; Get copies into a caller-supplied
// destination so the read hot path performs zero heap allocations and
// never shares mutable memory across goroutines.
package cache

import (
	"sync"
	"sync/atomic"

	"protowire/pool"
)

const (
	numShards          = 32
	shardMask          = numShards - 1
	defaultMaxPerShard = 256
)

type entry struct {
	key uint64
	buf *pool.Buffer // leased from the shared BufferPool
}

type shard struct {
	mu      sync.RWMutex
	entries map[uint64]*entry
	// clock-hand eviction: cheap O(1) amortized, no per-access bookkeeping
	// (no allocation, no list surgery) on the read path.
	order []uint64
	hand  int
}

// Cache is a sharded, fixed-capacity cache of opaque byte values.
type Cache struct {
	shards     [numShards]shard
	maxEntries int // per shard
	bufs       *pool.BufferPool

	gets      atomic.Int64
	hits      atomic.Int64
	evictions atomic.Int64
}

// New creates a Cache. maxEntries bounds the number of entries per shard;
// values are copied into buffers leased from bufs.
func New(bufs *pool.BufferPool, maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = defaultMaxPerShard
	}
	c := &Cache{maxEntries: maxEntries, bufs: bufs}
	for i := range c.shards {
		c.shards[i].entries = make(map[uint64]*entry, maxEntries)
		c.shards[i].order = make([]uint64, 0, maxEntries)
	}
	return c
}

func (c *Cache) shardFor(key uint64) *shard {
	// Fibonacci hashing spreads sequential keys across shards.
	h := key * 0x9E3779B97F4A7C15
	return &c.shards[(h>>32)&shardMask]
}

// Get copies the value for key into dst (grown via append semantics) and
// reports whether the key was present. The hot path allocates nothing when
// dst has sufficient capacity.
func (c *Cache) Get(key uint64, dst []byte) ([]byte, bool) {
	c.gets.Add(1)
	s := c.shardFor(key)
	s.mu.RLock()
	e, ok := s.entries[key]
	if !ok {
		s.mu.RUnlock()
		return dst, false
	}
	b := e.buf.Bytes()
	dst = append(dst, b...)
	s.mu.RUnlock()
	c.hits.Add(1)
	return dst, true
}

// Put stores a copy of value under key, evicting an older entry (clock
// order) when the shard is full. Safe for concurrent use.
func (c *Cache) Put(key uint64, value []byte) {
	s := c.shardFor(key)
	s.mu.Lock()
	if e, ok := s.entries[key]; ok {
		// Replace in place; Resize grows the lease only if it no longer fits.
		e.buf = e.buf.Resize(len(value))
		copy(e.buf.Bytes(), value)
		s.mu.Unlock()
		return
	}
	if len(s.entries) >= c.maxEntries {
		c.evictLocked(s)
	}
	buf := c.bufs.Get(len(value))
	buf = buf.Resize(len(value))
	copy(buf.Bytes(), value)
	s.entries[key] = &entry{key: key, buf: buf}
	s.order = append(s.order, key)
	s.mu.Unlock()
}

// evictLocked removes the entry under the clock hand. Caller holds s.mu.
func (c *Cache) evictLocked(s *shard) {
	if len(s.order) == 0 {
		return
	}
	victim := s.order[s.hand]
	s.hand = (s.hand + 1) % len(s.order)
	if e, ok := s.entries[victim]; ok {
		delete(s.entries, victim)
		_ = c.bufs.Release(e.buf)
		c.evictions.Add(1)
	}
	// Compact the order ring lazily: swap-remove the victim slot.
	for i, k := range s.order {
		if k == victim {
			s.order = append(s.order[:i], s.order[i+1:]...)
			if s.hand > i {
				s.hand--
			}
			if len(s.order) > 0 {
				s.hand %= len(s.order)
			} else {
				s.hand = 0
			}
			break
		}
	}
}

// Delete removes key if present, releasing its buffer to the pool.
func (c *Cache) Delete(key uint64) {
	s := c.shardFor(key)
	s.mu.Lock()
	if e, ok := s.entries[key]; ok {
		delete(s.entries, key)
		_ = c.bufs.Release(e.buf)
		for i, k := range s.order {
			if k == key {
				s.order = append(s.order[:i], s.order[i+1:]...)
				if s.hand > i {
					s.hand--
				}
				if len(s.order) > 0 {
					s.hand %= len(s.order)
				} else {
					s.hand = 0
				}
				break
			}
		}
	}
	s.mu.Unlock()
}

// Stats is a point-in-time snapshot of cache counters.
type Stats struct {
	Gets      int64
	Hits      int64
	Evictions int64
	Entries   int
}

// Stats returns current counters. Entries requires taking all shard locks;
// use for diagnostics and tests, not on the hot path.
func (c *Cache) Stats() Stats {
	st := Stats{
		Gets:      c.gets.Load(),
		Hits:      c.hits.Load(),
		Evictions: c.evictions.Load(),
	}
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.RLock()
		st.Entries += len(s.entries)
		s.mu.RUnlock()
	}
	return st
}

// Close releases every pooled buffer. The cache must not be used after
// Close; tests use it to assert zero leaks.
func (c *Cache) Close() {
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		for k, e := range s.entries {
			_ = c.bufs.Release(e.buf)
			delete(s.entries, k)
		}
		s.order = s.order[:0]
		s.hand = 0
		s.mu.Unlock()
	}
}
