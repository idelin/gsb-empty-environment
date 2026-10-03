package roaring

import "sync"

// Bitmap is a compressed bitmap of uint32 values. The high 16 bits of each
// value select a container; the low 16 bits are stored inside it. Container
// types (array / bitmap / run) are chosen automatically per 64k shard.
//
// Bitmap is safe for concurrent use.
type Bitmap struct {
	mu         sync.RWMutex
	containers map[uint16]container
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{containers: make(map[uint16]container)} }

// Add inserts a single id.
func (b *Bitmap) Add(id uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := uint16(id >> 16)
	c := b.containers[key]
	if c == nil {
		c = &arrayContainer{}
	}
	b.containers[key] = c.add(uint16(id))
}

// AddRange inserts every id in [key<<16|lo, key<<16|hi] (inclusive).
func (b *Bitmap) AddRange(key uint16, lo, hi uint16) {
	if lo > hi {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	rc := &runContainer{runs: []interval16{{start: lo, last: hi}}}
	c := b.containers[key]
	if c == nil {
		b.containers[key] = rc
		return
	}
	b.containers[key] = unionContainers(c, rc)
}

// Contains reports whether id is present.
func (b *Bitmap) Contains(id uint32) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c := b.containers[uint16(id>>16)]
	return c != nil && c.contains(uint16(id))
}

// Cardinality returns the number of values in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var n uint64
	for _, c := range b.containers {
		n += uint64(c.cardinality())
	}
	return n
}

// And returns the intersection of b and o. Neither operand is modified.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	if b == o {
		return b.Clone()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	for k, c := range b.containers {
		if oc, ok := o.containers[k]; ok {
			if r := intersectContainers(c, oc); r.cardinality() > 0 {
				out.containers[k] = r
			}
		}
	}
	return out
}

// Or returns the union of b and o. Neither operand is modified.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	if b == o {
		return b.Clone()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	for k, c := range b.containers {
		out.containers[k] = c.clone()
	}
	for k, c := range o.containers {
		if ec, ok := out.containers[k]; ok {
			out.containers[k] = unionContainers(ec, c)
		} else {
			out.containers[k] = c.clone()
		}
	}
	return out
}

// Xor returns the symmetric difference of b and o. Neither operand is modified.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	if b == o {
		return New()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	for k, c := range b.containers {
		if oc, ok := o.containers[k]; ok {
			if r := xorContainers(c, oc); r.cardinality() > 0 {
				out.containers[k] = r
			}
		} else {
			out.containers[k] = c.clone()
		}
	}
	for k, c := range o.containers {
		if _, ok := b.containers[k]; !ok {
			out.containers[k] = c.clone()
		}
	}
	return out
}

// Clone returns a deep copy of the bitmap.
func (b *Bitmap) Clone() *Bitmap {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := New()
	for k, c := range b.containers {
		out.containers[k] = c.clone()
	}
	return out
}

// ForEach calls fn for every id in the bitmap (unordered across shards).
func (b *Bitmap) ForEach(fn func(id uint32)) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for k, c := range b.containers {
		base := uint32(k) << 16
		c.foreach(func(x uint16) { fn(base | uint32(x)) })
	}
}

// Equals reports whether b and o contain exactly the same ids.
func (b *Bitmap) Equals(o *Bitmap) bool {
	if b.Cardinality() != o.Cardinality() {
		return false
	}
	eq := true
	b.ForEach(func(id uint32) {
		if eq && !o.Contains(id) {
			eq = false
		}
	})
	return eq
}

// Stats summarizes the container mix of the bitmap.
type Stats struct {
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
}

// Stats returns the container mix and total cardinality.
func (b *Bitmap) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var st Stats
	for _, c := range b.containers {
		switch c.(type) {
		case *arrayContainer:
			st.ArrayContainers++
		case *bitmapContainer:
			st.BitmapContainers++
		case *runContainer:
			st.RunContainers++
		}
		st.Cardinality += uint64(c.cardinality())
	}
	return st
}
