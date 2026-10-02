package wirebuf

import "sync"

// Cache is a fixed-capacity, direct-mapped, zero-allocation cache of
// decoded messages keyed by a 64-bit value (e.g. an FNV-1a hash of the
// encoded buffer). It never grows: inserting into an occupied slot
// evicts the previous entry and releases its message to the pool.
//
// Ownership: Put transfers ownership of the message to the cache.
// Take removes and returns the message, transferring ownership to the
// caller, who must Release it. Peek borrows the message only for the
// duration of fn, while the slot is locked.
//
// Cache is safe for concurrent use.
type Cache struct {
	mu    sync.RWMutex
	slots []cacheSlot
	mask  uint64
}

type cacheSlot struct {
	valid bool
	key   uint64
	msg   *Message
}

// NewCache creates a cache with 2^power slots. Allocation happens only
// here; steady-state Get/Put/Take perform no heap allocation.
func NewCache(power uint) *Cache {
	n := 1 << power
	return &Cache{
		slots: make([]cacheSlot, n),
		mask:  uint64(n - 1),
	}
}

// slot returns the slot index for a key using a multiplicative mix.
func (c *Cache) slot(key uint64) uint64 {
	return (key * 0x9E3779B97F4A7C15) & c.mask
}

// Put stores msg under key, evicting and releasing any previous entry
// in the slot. The cache takes ownership of msg.
func (c *Cache) Put(key uint64, msg *Message) {
	c.mu.Lock()
	i := c.slot(key)
	if s := &c.slots[i]; s.valid {
		s.msg.Release()
	}
	c.slots[i] = cacheSlot{valid: true, key: key, msg: msg}
	c.mu.Unlock()
}

// Take removes and returns the message stored under key, transferring
// ownership to the caller. Returns (nil, false) on a miss.
func (c *Cache) Take(key uint64) (*Message, bool) {
	c.mu.Lock()
	i := c.slot(key)
	s := &c.slots[i]
	if !s.valid || s.key != key {
		c.mu.Unlock()
		return nil, false
	}
	msg := s.msg
	*s = cacheSlot{}
	c.mu.Unlock()
	return msg, true
}

// Peek invokes fn with the cached message while holding the slot lock,
// so the message cannot be evicted concurrently. fn must not retain
// or mutate the message. Returns false on a miss.
func (c *Cache) Peek(key uint64, fn func(*Message)) bool {
	c.mu.RLock()
	i := c.slot(key)
	s := &c.slots[i]
	if !s.valid || s.key != key {
		c.mu.RUnlock()
		return false
	}
	fn(s.msg)
	c.mu.RUnlock()
	return true
}

// Len reports the number of occupied slots.
func (c *Cache) Len() int {
	c.mu.RLock()
	n := 0
	for i := range c.slots {
		if c.slots[i].valid {
			n++
		}
	}
	c.mu.RUnlock()
	return n
}

// Close evicts all entries and releases their messages to the pool.
func (c *Cache) Close() {
	c.mu.Lock()
	for i := range c.slots {
		if c.slots[i].valid {
			c.slots[i].msg.Release()
			c.slots[i] = cacheSlot{}
		}
	}
	c.mu.Unlock()
}

// FNV1a64 hashes b with the FNV-1a 64-bit algorithm, suitable as a
// cache key for encoded buffers. Allocation-free.
func FNV1a64(b []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return h
}
