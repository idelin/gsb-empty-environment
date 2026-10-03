package bitmap

import "sort"

// Bitmap is a compressed set of uint64 values. Values are split by their
// high 48 bits into containers holding the low 16 bits. Containers are
// stored in a hash map for O(1) access; a sorted key cache is rebuilt
// lazily for merge operations and iteration.
type Bitmap struct {
	m     map[uint64]container
	keys  []uint64
	cs    []container // aligned with keys; valid when !dirty
	dirty bool
}

// New returns an empty Bitmap.
func New() *Bitmap {
	return &Bitmap{m: make(map[uint64]container)}
}

func (b *Bitmap) ensureSorted() {
	if !b.dirty {
		return
	}
	b.keys = b.keys[:0]
	for k := range b.m {
		b.keys = append(b.keys, k)
	}
	sort.Slice(b.keys, func(i, j int) bool { return b.keys[i] < b.keys[j] })
	b.cs = make([]container, len(b.keys))
	for i, k := range b.keys {
		b.cs[i] = b.m[k]
	}
	b.dirty = false
}

// Add inserts a single value.
func (b *Bitmap) Add(x uint64) {
	k := x >> 16
	c, ok := b.m[k]
	if !ok {
		c = &arrayContainer{}
		b.dirty = true
	}
	prev := c
	c, _ = c.add(uint16(x))
	switch c.(type) {
	case *arrayContainer, *runContainer:
		c = optimize(c)
	}
	b.m[k] = c
	if c != prev {
		b.dirty = true
	}
}

// AddRange inserts every value in the inclusive range [lo, hi].
func (b *Bitmap) AddRange(lo, hi uint64) {
	if hi < lo {
		return
	}
	kLo, kHi := lo>>16, hi>>16
	for k := kLo; ; k++ {
		start := uint16(0)
		if k == kLo {
			start = uint16(lo)
		}
		end := uint16(0xffff)
		if k == kHi {
			end = uint16(hi)
		}
		c, ok := b.m[k]
		if !ok {
			c = &arrayContainer{}
			b.dirty = true
		}
		prev := c
		c, _ = c.addRange(start, end)
		c = optimize(c)
		b.m[k] = c
		if c != prev {
			b.dirty = true
		}
		if k == kHi {
			break
		}
	}
}

// Contains reports whether x is present.
func (b *Bitmap) Contains(x uint64) bool {
	c, ok := b.m[x>>16]
	return ok && c.contains(uint16(x))
}

// Cardinality returns the number of values in the set.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	for _, c := range b.m {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the set is empty.
func (b *Bitmap) IsEmpty() bool { return len(b.m) == 0 }

// And returns the intersection of b with all others (batch).
func (b *Bitmap) And(others ...*Bitmap) *Bitmap {
	result := b.Clone()
	for _, o := range others {
		result = andBitmaps(result, o)
	}
	return result
}

// Or returns the union of b with all others (batch).
func (b *Bitmap) Or(others ...*Bitmap) *Bitmap {
	result := b.Clone()
	for _, o := range others {
		result = orBitmaps(result, o)
	}
	return result
}

// Xor returns the symmetric difference of b with all others (batch).
func (b *Bitmap) Xor(others ...*Bitmap) *Bitmap {
	result := b.Clone()
	for _, o := range others {
		result = xorBitmaps(result, o)
	}
	return result
}

// AndCardinality returns the size of the intersection without
// materializing it.
func (b *Bitmap) AndCardinality(others ...*Bitmap) uint64 {
	if len(others) == 0 {
		return b.Cardinality()
	}
	if len(others) > 1 {
		return b.And(others...).Cardinality()
	}
	o := others[0]
	b.ensureSorted()
	o.ensureSorted()
	var total uint64
	i, j := 0, 0
	for i < len(b.keys) && j < len(o.keys) {
		switch {
		case b.keys[i] < o.keys[j]:
			i++
		case b.keys[i] > o.keys[j]:
			j++
		default:
			total += uint64(andCardContainers(b.cs[i], o.cs[j]))
			i++
			j++
		}
	}
	return total
}

func andBitmaps(a, b *Bitmap) *Bitmap {
	a.ensureSorted()
	b.ensureSorted()
	out := New()
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] < b.keys[j]:
			i++
		case a.keys[i] > b.keys[j]:
			j++
		default:
			c := optimize(andContainers(a.cs[i], b.cs[j]))
			if c.cardinality() > 0 {
				out.m[a.keys[i]] = c
			}
			i++
			j++
		}
	}
	out.dirty = true
	return out
}

func orBitmaps(a, b *Bitmap) *Bitmap {
	a.ensureSorted()
	b.ensureSorted()
	out := New()
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] < b.keys[j]:
			out.m[a.keys[i]] = a.cs[i].clone()
			i++
		case a.keys[i] > b.keys[j]:
			out.m[b.keys[j]] = b.cs[j].clone()
			j++
		default:
			out.m[a.keys[i]] = optimize(orContainers(a.cs[i], b.cs[j]))
			i++
			j++
		}
	}
	for ; i < len(a.keys); i++ {
		out.m[a.keys[i]] = a.cs[i].clone()
	}
	for ; j < len(b.keys); j++ {
		out.m[b.keys[j]] = b.cs[j].clone()
	}
	out.dirty = true
	return out
}

func xorBitmaps(a, b *Bitmap) *Bitmap {
	a.ensureSorted()
	b.ensureSorted()
	out := New()
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] < b.keys[j]:
			out.m[a.keys[i]] = a.cs[i].clone()
			i++
		case a.keys[i] > b.keys[j]:
			out.m[b.keys[j]] = b.cs[j].clone()
			j++
		default:
			c := optimize(xorContainers(a.cs[i], b.cs[j]))
			if c.cardinality() > 0 {
				out.m[a.keys[i]] = c
			}
			i++
			j++
		}
	}
	for ; i < len(a.keys); i++ {
		out.m[a.keys[i]] = a.cs[i].clone()
	}
	for ; j < len(b.keys); j++ {
		out.m[b.keys[j]] = b.cs[j].clone()
	}
	out.dirty = true
	return out
}

// Clone returns a deep copy.
func (b *Bitmap) Clone() *Bitmap {
	out := New()
	for k, c := range b.m {
		out.m[k] = c.clone()
	}
	out.dirty = true
	return out
}

// Equals reports whether both bitmaps contain the same values.
func (b *Bitmap) Equals(o *Bitmap) bool {
	if len(b.m) != len(o.m) {
		return false
	}
	for k, c := range b.m {
		oc, ok := o.m[k]
		if !ok || !c.equals(oc) {
			return false
		}
	}
	return true
}

// Optimize re-selects the most compact container encoding for every key.
func (b *Bitmap) Optimize() {
	for k, c := range b.m {
		b.m[k] = optimize(c)
	}
}

// Stats summarizes container usage and memory footprint.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
	Bytes            int
}

// Stats computes per-container-type statistics.
func (b *Bitmap) Stats() Stats {
	s := Stats{Containers: len(b.m)}
	for _, c := range b.m {
		card := c.cardinality()
		s.Cardinality += uint64(card)
		switch t := c.(type) {
		case *arrayContainer:
			s.ArrayContainers++
			s.Bytes += 2 * card
		case *bitmapContainer:
			s.BitmapContainers++
			s.Bytes += bitmapContainerBytes
		case *runContainer:
			s.RunContainers++
			s.Bytes += runLengthOverhead * len(t.runs)
		}
	}
	return s
}

// Iterator iterates values in ascending order.
type Iterator struct {
	keys []uint64
	cs   []container
	ki   int
	ci   containerIterator
}

// Iterator returns an iterator over all values in ascending order.
func (b *Bitmap) Iterator() *Iterator {
	b.ensureSorted()
	it := &Iterator{keys: b.keys, cs: b.cs}
	if len(b.keys) > 0 {
		it.ci = b.cs[0].iter()
	}
	return it
}

// Next returns the next value and true, or false when exhausted.
func (it *Iterator) Next() (uint64, bool) {
	for it.ci != nil {
		if v, ok := it.ci.next(); ok {
			return it.keys[it.ki]<<16 | uint64(v), true
		}
		it.ki++
		if it.ki >= len(it.keys) {
			return 0, false
		}
		it.ci = it.cs[it.ki].iter()
	}
	return 0, false
}
