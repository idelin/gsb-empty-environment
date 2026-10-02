// Package pool provides GC-friendly object pools and a fixed-capacity,
// zero-allocation cache for high-frequency hot paths.
//
// Design goals:
//   - steady-state Get/Put on the cache perform zero heap allocations;
//   - pooled objects are tracked with an outstanding counter so tests can
//     assert strict absence of leaks;
//   - everything is safe for concurrent use.
package pool

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrCacheFull is returned by Cache.Put when the cache is full and eviction
// is disabled.
var ErrCacheFull = errors.New("pool: cache full")

// ErrValueTooLarge is returned when a value exceeds the cache slot size.
var ErrValueTooLarge = errors.New("pool: value exceeds slot size")

// ---------------------------------------------------------------------------
// Buffer pool
// ---------------------------------------------------------------------------

// Buffer is a reusable byte buffer obtained from BufferPool.
type Buffer struct {
	Buf []byte
}

// Reset returns the buffer to a zero-length state, keeping capacity.
func (b *Buffer) Reset() { b.Buf = b.Buf[:0] }

var buffers = sync.Pool{New: func() any { return &Buffer{Buf: make([]byte, 0, 512)} }}

// GetBuffer acquires a reusable buffer with at least sizeHint capacity.
func GetBuffer(sizeHint int) *Buffer {
	b := buffers.Get().(*Buffer)
	if cap(b.Buf) < sizeHint {
		b.Buf = make([]byte, 0, sizeHint)
	}
	b.Reset()
	return b
}

// PutBuffer returns a buffer to the pool. Oversized buffers are dropped to
// avoid pinning large allocations in the pool.
func PutBuffer(b *Buffer) {
	if b == nil || cap(b.Buf) > 1<<20 {
		return
	}
	b.Reset()
	buffers.Put(b)
}

// ---------------------------------------------------------------------------
// Entry pool with leak tracking
// ---------------------------------------------------------------------------

// Entry is a reusable log-entry value container used on the Paxos hot path.
type Entry struct {
	Slot  uint64
	Value []byte
	Noop  bool
}

// EntryPool recycles Entry values through a deterministic free list (unlike
// sync.Pool, reuse survives GCs, which keeps reuse semantics testable). It
// tracks the number of outstanding (acquired but not released) entries so
// tests can assert zero leaks.
type EntryPool struct {
	valueCap    int
	mu          sync.Mutex
	free        []*Entry
	outstanding atomic.Int64
	recycled    atomic.Int64
}

// NewEntryPool creates a pool whose entries carry values of valueCap bytes.
func NewEntryPool(valueCap int) *EntryPool {
	return &EntryPool{valueCap: valueCap}
}

// Acquire returns an Entry, reusing memory when possible.
func (p *EntryPool) Acquire() *Entry {
	p.mu.Lock()
	if n := len(p.free); n > 0 {
		e := p.free[n-1]
		p.free = p.free[:n-1]
		p.mu.Unlock()
		e.Slot = 0
		e.Noop = false
		e.Value = e.Value[:0]
		p.outstanding.Add(1)
		return e
	}
	p.mu.Unlock()
	p.outstanding.Add(1)
	return &Entry{Value: make([]byte, 0, p.valueCap)}
}

// Release returns an Entry to the pool. Releasing the same entry twice is a
// programming error and is detected via the outstanding counter going
// negative in tests.
func (p *EntryPool) Release(e *Entry) {
	if e == nil {
		return
	}
	p.outstanding.Add(-1)
	p.recycled.Add(1)
	p.mu.Lock()
	p.free = append(p.free, e)
	p.mu.Unlock()
}

// Outstanding reports entries currently checked out (leak detector).
func (p *EntryPool) Outstanding() int64 { return p.outstanding.Load() }

// Recycled reports how many entries have been returned for reuse.
func (p *EntryPool) Recycled() int64 { return p.recycled.Load() }

// ---------------------------------------------------------------------------
// Zero-allocation fixed-capacity cache
// ---------------------------------------------------------------------------

// Cache is a fixed-capacity, fixed-slot-size key/value cache keyed by
// uint64. After construction it performs no heap allocations on Get, Put or
// Delete: values are copied in and out of preallocated slots, and the index
// map is preallocated with full capacity so steady-state inserts do not grow
// it. A round-robin cursor evicts the oldest-written slot when full.
type Cache struct {
	mu      sync.Mutex
	slots   []cacheSlot
	index   map[uint64]int
	free    []int
	victim  int
	maxVal  int
	evictOK bool
	hits    atomic.Uint64
	misses  atomic.Uint64
	evicted atomic.Uint64
}

type cacheSlot struct {
	key     uint64
	val     []byte
	length  int
	used    bool
	version uint64
}

// NewCache builds a cache with capacity slots of maxValueSize bytes each.
// If evict is false, Put on a full cache returns ErrCacheFull instead of
// evicting.
func NewCache(capacity, maxValueSize int, evict bool) *Cache {
	if capacity <= 0 || maxValueSize <= 0 {
		panic("pool: capacity and maxValueSize must be positive")
	}
	c := &Cache{
		slots:   make([]cacheSlot, capacity),
		index:   make(map[uint64]int, capacity),
		free:    make([]int, 0, capacity),
		maxVal:  maxValueSize,
		evictOK: evict,
	}
	for i := range c.slots {
		c.slots[i].val = make([]byte, maxValueSize)
		c.free = append(c.free, i)
	}
	return c
}

// Put stores a copy of val under key. Overwriting an existing key reuses the
// slot in place. On a full cache it evicts (round-robin) or fails.
func (c *Cache) Put(key uint64, val []byte) error {
	if len(val) > c.maxVal {
		return ErrValueTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if i, ok := c.index[key]; ok {
		s := &c.slots[i]
		s.length = copy(s.val, val)
		s.version++
		return nil
	}
	var i int
	if n := len(c.free); n > 0 {
		i = c.free[n-1]
		c.free = c.free[:n-1]
	} else if c.evictOK {
		i = c.victim
		c.victim = (c.victim + 1) % len(c.slots)
		delete(c.index, c.slots[i].key)
		c.evicted.Add(1)
	} else {
		return ErrCacheFull
	}
	s := &c.slots[i]
	s.key = key
	s.used = true
	s.length = copy(s.val, val)
	s.version++
	c.index[key] = i
	return nil
}

// Get copies the value for key into dst and returns its length. It never
// allocates; dst is grown by the caller, not here.
func (c *Cache) Get(key uint64, dst []byte) (int, bool) {
	c.mu.Lock()
	i, ok := c.index[key]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return 0, false
	}
	s := &c.slots[i]
	n := copy(dst, s.val[:s.length])
	c.mu.Unlock()
	c.hits.Add(1)
	return n, true
}

// Delete removes key, returning its slot to the free list.
func (c *Cache) Delete(key uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.index[key]
	if !ok {
		return false
	}
	delete(c.index, key)
	c.slots[i].used = false
	c.free = append(c.free, i)
	return true
}

// Len reports the number of live entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.index)
}

// Stats exposes hit/miss/eviction counters.
func (c *Cache) Stats() (hits, misses, evicted uint64) {
	return c.hits.Load(), c.misses.Load(), c.evicted.Load()
}
