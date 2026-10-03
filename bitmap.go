// Package cbitmap implements a compressed 32-bit bitmap (Roaring-style)
// with array, bitmap and run-length-encoded containers, binary compact
// serialization and zero-copy (memory-mappable) deserialization.
//
// A Bitmap is not safe for concurrent mutation. A frozen Bitmap
// (obtained from UnmarshalZeroCopy or Freeze) is immutable from the
// caller's perspective and safe for concurrent reads; any mutating call
// on a frozen Bitmap first performs a deep copy (copy-on-write).
package cbitmap

import "sort"

// Bitmap is a compressed set of uint32 values.
type Bitmap struct {
	keys   []uint16    // high 16 bits, sorted
	cons   []container // low 16 bits containers, aligned with keys
	frozen bool
}

// New returns an empty bitmap.
func New() *Bitmap { return &Bitmap{} }

func (bm *Bitmap) search(key uint16) (int, bool) {
	i := sort.Search(len(bm.keys), func(i int) bool { return bm.keys[i] >= key })
	return i, i < len(bm.keys) && bm.keys[i] == key
}

func (bm *Bitmap) insertAt(i int, key uint16, c container) {
	bm.keys = append(bm.keys, 0)
	copy(bm.keys[i+1:], bm.keys[i:])
	bm.keys[i] = key
	bm.cons = append(bm.cons, nil)
	copy(bm.cons[i+1:], bm.cons[i:])
	bm.cons[i] = c
}

// thaw materializes a private copy of all containers when the bitmap is
// frozen (copy-on-write), so mutations never touch shared buffers.
func (bm *Bitmap) thaw() {
	if !bm.frozen {
		return
	}
	keys := make([]uint16, len(bm.keys))
	copy(keys, bm.keys)
	cons := make([]container, len(bm.cons))
	for i, c := range bm.cons {
		cons[i] = c.clone()
	}
	bm.keys, bm.cons = keys, cons
	bm.frozen = false
}

// Freeze marks the bitmap immutable-from-the-outside: subsequent
// mutations transparently copy the containers first. A frozen bitmap is
// safe for concurrent reads.
func (bm *Bitmap) Freeze() { bm.frozen = true }

// Frozen reports whether the bitmap is frozen.
func (bm *Bitmap) Frozen() bool { return bm.frozen }

// Clone returns a deep copy of the bitmap.
func (bm *Bitmap) Clone() *Bitmap {
	out := &Bitmap{
		keys: make([]uint16, len(bm.keys)),
		cons: make([]container, len(bm.cons)),
	}
	copy(out.keys, bm.keys)
	for i, c := range bm.cons {
		out.cons[i] = c.clone()
	}
	return out
}

// Add inserts a single value.
func (bm *Bitmap) Add(x uint32) {
	hi, lo := uint16(x>>16), uint16(x)
	bm.thaw()
	i, found := bm.search(hi)
	if !found {
		bm.insertAt(i, hi, &arrayContainer{data: []uint16{lo}})
		return
	}
	bm.cons[i] = containerAdd(bm.cons[i], lo)
}

func containerAdd(c container, x uint16) container {
	switch c := c.(type) {
	case *arrayContainer:
		data, added := arrayInsert(c.data, x)
		if !added {
			return c
		}
		ac := &arrayContainer{data: data}
		if len(data) > arrayMaxSize {
			return ac.toBitmap()
		}
		return ac
	case *bitmapContainer:
		w := &c.words[x>>6]
		m := uint64(1) << (x & 63)
		if *w&m == 0 {
			*w |= m
			if c.card >= 0 {
				c.card++
			}
		}
		return c
	case *runContainer:
		c.add(x)
		return c
	}
	panic("cbitmap: unknown container type")
}

func arrayInsert(data []uint16, x uint16) ([]uint16, bool) {
	i := sort.Search(len(data), func(i int) bool { return data[i] >= x })
	if i < len(data) && data[i] == x {
		return data, false
	}
	data = append(data, 0)
	copy(data[i+1:], data[i:])
	data[i] = x
	return data, true
}

// AddRange inserts all values in the half-open interval [lo, hi).
func (bm *Bitmap) AddRange(lo, hi uint32) {
	if lo >= hi {
		return
	}
	bm.thaw()
	last := hi - 1
	k0, k1 := lo>>16, last>>16
	for k := k0; k <= k1; k++ {
		rlo := uint16(0)
		if k == k0 {
			rlo = uint16(lo)
		}
		rhi := uint16(0xffff)
		if k == k1 {
			rhi = uint16(last)
		}
		key := uint16(k)
		rc := &runContainer{runs: []run16{{start: rlo, length: rhi - rlo}}, card: -1}
		i, found := bm.search(key)
		if !found {
			bm.insertAt(i, key, rc.optimize())
			continue
		}
		bm.cons[i] = or(bm.cons[i], rc)
	}
}

// Contains reports whether x is present.
func (bm *Bitmap) Contains(x uint32) bool {
	i, found := bm.search(uint16(x >> 16))
	return found && bm.cons[i].contains(uint16(x))
}

// Cardinality returns the number of values in the set.
func (bm *Bitmap) Cardinality() uint64 {
	var n uint64
	for _, c := range bm.cons {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the bitmap has no values.
func (bm *Bitmap) IsEmpty() bool { return len(bm.keys) == 0 }

// And returns the intersection of a and b.
func And(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] == b.keys[j]:
			c := and(a.cons[i], b.cons[j])
			if c.cardinality() > 0 {
				out.keys = append(out.keys, a.keys[i])
				out.cons = append(out.cons, c)
			}
			i++
			j++
		case a.keys[i] < b.keys[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// Or returns the union of a and b.
func Or(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] == b.keys[j]:
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, or(a.cons[i], b.cons[j]))
			i++
			j++
		case a.keys[i] < b.keys[j]:
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, a.cons[i].clone())
			i++
		default:
			out.keys = append(out.keys, b.keys[j])
			out.cons = append(out.cons, b.cons[j].clone())
			j++
		}
	}
	for ; i < len(a.keys); i++ {
		out.keys = append(out.keys, a.keys[i])
		out.cons = append(out.cons, a.cons[i].clone())
	}
	for ; j < len(b.keys); j++ {
		out.keys = append(out.keys, b.keys[j])
		out.cons = append(out.cons, b.cons[j].clone())
	}
	return out
}

// Xor returns the symmetric difference of a and b.
func Xor(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] == b.keys[j]:
			c := xor(a.cons[i], b.cons[j])
			if c.cardinality() > 0 {
				out.keys = append(out.keys, a.keys[i])
				out.cons = append(out.cons, c)
			}
			i++
			j++
		case a.keys[i] < b.keys[j]:
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, a.cons[i].clone())
			i++
		default:
			out.keys = append(out.keys, b.keys[j])
			out.cons = append(out.cons, b.cons[j].clone())
			j++
		}
	}
	for ; i < len(a.keys); i++ {
		out.keys = append(out.keys, a.keys[i])
		out.cons = append(out.cons, a.cons[i].clone())
	}
	for ; j < len(b.keys); j++ {
		out.keys = append(out.keys, b.keys[j])
		out.cons = append(out.cons, b.cons[j].clone())
	}
	return out
}

// AndAll intersects all given bitmaps (batch AND).
func AndAll(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	out := bs[0]
	for _, b := range bs[1:] {
		out = And(out, b)
	}
	return out
}

// OrAll unions all given bitmaps (batch OR).
func OrAll(bs ...*Bitmap) *Bitmap {
	out := New()
	for _, b := range bs {
		out = Or(out, b)
	}
	return out
}

// XorAll folds all given bitmaps with XOR (batch XOR).
func XorAll(bs ...*Bitmap) *Bitmap {
	out := New()
	for _, b := range bs {
		out = Xor(out, b)
	}
	return out
}

// Iterator iterates over the values of a bitmap in increasing order.
type Iterator struct {
	bm  *Bitmap
	ci  int
	cur containerIterator
}

// Iterator returns an iterator over the bitmap values.
func (bm *Bitmap) Iterator() *Iterator { return &Iterator{bm: bm} }

// Next returns the next value and true, or false when exhausted.
func (it *Iterator) Next() (uint32, bool) {
	for {
		if it.cur == nil {
			if it.ci >= len(it.bm.cons) {
				return 0, false
			}
			it.cur = it.bm.cons[it.ci].iterator()
			it.ci++
		}
		if v, ok := it.cur.next(); ok {
			return uint32(it.bm.keys[it.ci-1])<<16 | uint32(v), true
		}
		it.cur = nil
	}
}

// ToArray materializes all values in increasing order.
func (bm *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, bm.Cardinality())
	it := bm.Iterator()
	for v, ok := it.Next(); ok; v, ok = it.Next() {
		out = append(out, v)
	}
	return out
}

// Stats summarizes the container composition of a bitmap.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
	// PayloadBytes is the sum of container payload sizes.
	PayloadBytes int
}

// Stats computes container statistics.
func (bm *Bitmap) Stats() Stats {
	var s Stats
	s.Containers = len(bm.cons)
	for _, c := range bm.cons {
		switch c.typeID() {
		case arrayContainerID:
			s.ArrayContainers++
		case bitmapContainerID:
			s.BitmapContainers++
		case runContainerID:
			s.RunContainers++
		}
		s.Cardinality += uint64(c.cardinality())
		s.PayloadBytes += c.dataSize()
	}
	return s
}
