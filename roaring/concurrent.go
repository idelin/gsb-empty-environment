package roaring

import "sync"

// ConcurrentBitmap wraps a Bitmap with a read-write mutex so it can be
// shared by multiple goroutines.
type ConcurrentBitmap struct {
	mu sync.RWMutex
	bm *Bitmap
}

// NewConcurrent returns an empty concurrent bitmap.
func NewConcurrent() *ConcurrentBitmap {
	return &ConcurrentBitmap{bm: New()}
}

// Add inserts a single ID.
func (c *ConcurrentBitmap) Add(v uint32) {
	c.mu.Lock()
	c.bm.Add(v)
	c.mu.Unlock()
}

// AddRange inserts all IDs in [start, end).
func (c *ConcurrentBitmap) AddRange(start, end uint64) {
	c.mu.Lock()
	c.bm.AddRange(start, end)
	c.mu.Unlock()
}

// Remove deletes a single ID.
func (c *ConcurrentBitmap) Remove(v uint32) {
	c.mu.Lock()
	c.bm.Remove(v)
	c.mu.Unlock()
}

// Contains reports whether v is present.
func (c *ConcurrentBitmap) Contains(v uint32) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bm.Contains(v)
}

// Cardinality returns the number of IDs.
func (c *ConcurrentBitmap) Cardinality() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bm.Cardinality()
}

// Snapshot returns a deep copy safe to use without locks.
func (c *ConcurrentBitmap) Snapshot() *Bitmap {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bm.Clone()
}

// And returns the intersection of consistent snapshots of both bitmaps.
func (c *ConcurrentBitmap) And(o *ConcurrentBitmap) *Bitmap {
	return c.Snapshot().And(o.Snapshot())
}

// Or returns the union of consistent snapshots of both bitmaps.
func (c *ConcurrentBitmap) Or(o *ConcurrentBitmap) *Bitmap {
	return c.Snapshot().Or(o.Snapshot())
}

// Xor returns the symmetric difference of consistent snapshots.
func (c *ConcurrentBitmap) Xor(o *ConcurrentBitmap) *Bitmap {
	return c.Snapshot().Xor(o.Snapshot())
}
