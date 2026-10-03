package bitmapckpt

import "sync"

// Bitmap is a compressed bitmap over uint64 values, split into 16-bit-keyed
// containers (key = value >> 16). It is safe for concurrent use. Containers are copy-on-write,
// which makes snapshots and checkpoints cheap and keeps snapshot pauses
// proportional to the number of containers modified since the last snapshot.
type Bitmap struct {
	mu    sync.RWMutex
	cs    map[uint64]container
	dirty map[uint64]struct{} // containers modified since last snapshot/checkpoint
	ver   uint64              // bumped by every Snapshot/Checkpoint
}

// New returns an empty bitmap.
func New() *Bitmap {
	return &Bitmap{
		cs:    make(map[uint64]container),
		dirty: make(map[uint64]struct{}),
	}
}

// writable returns the container for key, cloning it first if it is shared
// with a snapshot, checkpoint or another bitmap.
func (b *Bitmap) writable(key uint64) container {
	c := b.cs[key]
	if c == nil {
		return newArrayContainer()
	}
	if c.isShared() {
		return c.clone()
	}
	return c
}

// Add inserts a single value.
func (b *Bitmap) Add(v uint64) {
	key := v >> 16
	b.mu.Lock()
	b.cs[key] = b.writable(key).add(uint16(v))
	b.dirty[key] = struct{}{}
	b.mu.Unlock()
}

// AddRange inserts all values in [lo, hi).
func (b *Bitmap) AddRange(lo, hi uint64) {
	if lo >= hi {
		return
	}
	b.mu.Lock()
	firstKey, lastKey := lo>>16, (hi-1)>>16
	for key := firstKey; key <= lastKey; key++ {
		base := key << 16
		lo16 := uint16(0)
		if lo > base {
			lo16 = uint16(lo - base)
		}
		hi16 := uint16(0xffff)
		if hi-1 < base+0xffff {
			hi16 = uint16(hi - 1 - base)
		}
		if b.cs[key] == nil {
			// Fresh container holding a contiguous interval: a run is
			// always the optimal representation.
			rc := &runContainer{runs: []interval{{lo16, hi16}}}
			rc.card = int(hi16) - int(lo16) + 1
			b.cs[key] = rc
		} else {
			b.cs[key] = b.writable(key).addRange(lo16, hi16)
		}
		b.dirty[key] = struct{}{}
	}
	b.mu.Unlock()
}

// Contains reports whether v is present.
func (b *Bitmap) Contains(v uint64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c := b.cs[v>>16]
	return c != nil && c.contains(uint16(v))
}

// Cardinality returns the number of values in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	b.mu.RLock()
	var n uint64
	for _, c := range b.cs {
		n += uint64(c.cardinality())
	}
	b.mu.RUnlock()
	return n
}

// ForEach calls fn for every value in ascending order, stopping early if fn
// returns false. The bitmap is read-locked during iteration, so fn must not
// call back into the bitmap.
func (b *Bitmap) ForEach(fn func(uint64) bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	keys := make([]uint64, 0, len(b.cs))
	for k := range b.cs {
		keys = append(keys, k)
	}
	sortUint64(keys)
	for _, k := range keys {
		base := k << 16
		stop := false
		b.cs[k].foreach(func(v uint16) bool {
			if !fn(base | uint64(v)) {
				stop = true
				return false
			}
			return true
		})
		if stop {
			return
		}
	}
}

func sortUint64(s []uint64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Clone returns an independent copy. Containers are shared lazily via
// copy-on-write, so cloning is O(number of containers) pointer copies.
func (b *Bitmap) Clone() *Bitmap {
	b.mu.Lock()
	out := New()
	out.ver = b.ver
	for k, c := range b.cs {
		c.setShared(true)
		out.cs[k] = c
	}
	b.mu.Unlock()
	return out
}

// Optimize re-selects the most compact container type for every key.
func (b *Bitmap) Optimize() {
	b.mu.Lock()
	for k, c := range b.cs {
		var best container
		switch c.typeID() {
		case typeArray:
			a := c.(*arrayContainer)
			r := a.toRun()
			if 4*len(r.runs) < 2*len(a.data) {
				best = r
			}
		case typeBitmap:
			bc := c.(*bitmapContainer)
			r := bc.toRun()
			if 4*len(r.runs) < 8*bitmapWords {
				best = r
			}
		}
		if best != nil {
			b.cs[k] = best
			b.dirty[k] = struct{}{}
		}
	}
	b.mu.Unlock()
}

// ContainerStats reports how many containers of each type are in use.
func (b *Bitmap) ContainerStats() (arrays, bitmaps, runs int) {
	b.mu.RLock()
	for _, c := range b.cs {
		switch c.typeID() {
		case typeArray:
			arrays++
		case typeBitmap:
			bitmaps++
		case typeRun:
			runs++
		}
	}
	b.mu.RUnlock()
	return
}

// NumContainers returns the number of containers.
func (b *Bitmap) NumContainers() int {
	b.mu.RLock()
	n := len(b.cs)
	b.mu.RUnlock()
	return n
}

// And returns the intersection of b and o.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	if b == o {
		return b.Clone()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	small, large := b, o
	if len(o.cs) < len(b.cs) {
		small, large = o, b
	}
	out.cs = make(map[uint64]container, len(small.cs))
	for k, c := range small.cs {
		if oc, ok := large.cs[k]; ok {
			if nc := andContainers(c, oc); nc.cardinality() > 0 {
				out.cs[k] = nc
			}
		}
	}
	return out
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	if b == o {
		return b.Clone()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	out.cs = make(map[uint64]container, len(b.cs)+len(o.cs))
	for k, c := range b.cs {
		if oc, ok := o.cs[k]; ok {
			out.cs[k] = orContainers(c, oc)
		} else {
			out.cs[k] = c.clone()
		}
	}
	for k, c := range o.cs {
		if _, ok := b.cs[k]; !ok {
			out.cs[k] = c.clone()
		}
	}
	return out
}

// Xor returns the symmetric difference of b and o.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	if b == o {
		return New()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := New()
	out.cs = make(map[uint64]container, len(b.cs)+len(o.cs))
	for k, c := range b.cs {
		if oc, ok := o.cs[k]; ok {
			if nc := xorContainers(c, oc); nc.cardinality() > 0 {
				out.cs[k] = nc
			}
		} else {
			out.cs[k] = c.clone()
		}
	}
	for k, c := range o.cs {
		if _, ok := b.cs[k]; !ok {
			out.cs[k] = c.clone()
		}
	}
	return out
}

// Equals reports whether b and o contain exactly the same values.
func (b *Bitmap) Equals(o *Bitmap) bool {
	return b.Xor(o).Cardinality() == 0
}

// And returns the intersection of all given bitmaps (batch operation).
func And(bs ...*Bitmap) *Bitmap {
	switch len(bs) {
	case 0:
		return New()
	case 1:
		return bs[0].Clone()
	}
	acc := bs[0].And(bs[1])
	for _, x := range bs[2:] {
		acc = acc.And(x)
	}
	return acc
}

// Or returns the union of all given bitmaps (batch operation).
func Or(bs ...*Bitmap) *Bitmap {
	switch len(bs) {
	case 0:
		return New()
	case 1:
		return bs[0].Clone()
	}
	acc := bs[0].Or(bs[1])
	for _, x := range bs[2:] {
		acc = acc.Or(x)
	}
	return acc
}

// Xor returns the symmetric difference of all given bitmaps (batch operation).
func Xor(bs ...*Bitmap) *Bitmap {
	switch len(bs) {
	case 0:
		return New()
	case 1:
		return bs[0].Clone()
	}
	acc := bs[0].Xor(bs[1])
	for _, x := range bs[2:] {
		acc = acc.Xor(x)
	}
	return acc
}
