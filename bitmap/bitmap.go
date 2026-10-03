// Package bitmap implements a compressed bitmap index inspired by
// Roaring Bitmaps. The 32-bit key space is split by the high 16 bits
// into containers; each container automatically picks the most compact
// of three encodings: sorted array, 64Kbit bitmap, or run-length
// encoded intervals.
package bitmap

import "sort"

// Bitmap is a compressed set of uint32 values.
// It is not safe for concurrent mutation; callers must synchronize.
type Bitmap struct {
	keys  []uint16 // sorted high-16-bit keys
	conts []container
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

func (b *Bitmap) search(key uint16) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	return i, i < len(b.keys) && b.keys[i] == key
}

func (b *Bitmap) insertAt(i int, key uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = key
	b.conts = append(b.conts, nil)
	copy(b.conts[i+1:], b.conts[i:])
	b.conts[i] = c
}

// Add inserts a single value.
func (b *Bitmap) Add(x uint32) {
	key, low := uint16(x>>16), uint16(x)
	i, ok := b.search(key)
	if !ok {
		b.insertAt(i, key, &arrayContainer{data: []uint16{low}})
		return
	}
	b.conts[i] = b.conts[i].add(low)
}

// AddRange inserts every value in [lo, hi). hi may be 1<<32.
func (b *Bitmap) AddRange(lo, hi uint64) {
	if lo >= hi || lo >= 1<<32 {
		return
	}
	if hi > 1<<32 {
		hi = 1 << 32
	}
	hi-- // inclusive
	kLo, kHi := uint16(lo>>16), uint16(hi>>16)
	for k := kLo; ; k++ {
		start, last := uint16(0), uint16(0xFFFF)
		if k == kLo {
			start = uint16(lo)
		}
		if k == kHi {
			last = uint16(hi)
		}
		b.addRangeToContainer(k, start, last)
		if k == kHi {
			break
		}
	}
}

func (b *Bitmap) addRangeToContainer(key uint16, start, last uint16) {
	rc := &runContainer{runs: []run{{start, last}}}
	i, ok := b.search(key)
	if !ok {
		b.conts = append(b.conts, nil)
		b.keys = append(b.keys, 0)
		copy(b.keys[i+1:], b.keys[i:])
		copy(b.conts[i+1:], b.conts[i:])
		b.keys[i], b.conts[i] = key, rc
		return
	}
	b.conts[i] = orContainers(b.conts[i], rc).optimize()
}

// Contains reports whether x is present.
func (b *Bitmap) Contains(x uint32) bool {
	i, ok := b.search(uint16(x >> 16))
	return ok && b.conts[i].contains(uint16(x))
}

// Cardinality returns the number of values in the set.
func (b *Bitmap) Cardinality() int64 {
	var n int64
	for _, c := range b.conts {
		n += int64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the set is empty.
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// Optimize re-encodes every container into its most compact form.
func (b *Bitmap) Optimize() {
	for i, c := range b.conts {
		b.conts[i] = c.optimize()
	}
}

// Clone returns a deep copy.
func (b *Bitmap) Clone() *Bitmap {
	out := &Bitmap{
		keys:  make([]uint16, len(b.keys)),
		conts: make([]container, len(b.conts)),
	}
	copy(out.keys, b.keys)
	for i, c := range b.conts {
		out.conts[i] = c.clone()
	}
	return out
}

// ToSlice returns all values in ascending order. Intended for
// small sets (tests, debugging).
func (b *Bitmap) ToSlice() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	for i, k := range b.keys {
		base := uint32(k) << 16
		for _, v := range b.conts[i].toSlice() {
			out = append(out, base|uint32(v))
		}
	}
	return out
}

// ContainerSummary counts containers by encoding kind.
func (b *Bitmap) ContainerSummary() (arrays, bitmaps, runs int) {
	for _, c := range b.conts {
		switch c.(type) {
		case *arrayContainer:
			arrays++
		case *bitmapContainer:
			bitmaps++
		case *runContainer:
			runs++
		}
	}
	return
}

// And returns the intersection of a and b.
func And(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	capHint := len(a.keys)
	if len(b.keys) < capHint {
		capHint = len(b.keys)
	}
	out.keys = make([]uint16, 0, capHint)
	out.conts = make([]container, 0, capHint)
	ia, ib := 0, 0
	for ia < len(a.keys) && ib < len(b.keys) {
		ka, kb := a.keys[ia], b.keys[ib]
		switch {
		case ka < kb:
			ia++
		case ka > kb:
			ib++
		default:
			if c := andContainers(a.conts[ia], b.conts[ib]); c.cardinality() > 0 {
				out.keys = append(out.keys, ka)
				out.conts = append(out.conts, c)
			}
			ia++
			ib++
		}
	}
	return out
}

// Or returns the union of a and b.
func Or(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	out.keys = make([]uint16, 0, len(a.keys)+len(b.keys))
	out.conts = make([]container, 0, len(a.keys)+len(b.keys))
	ia, ib := 0, 0
	appendClone := func(src *Bitmap, i int) {
		out.keys = append(out.keys, src.keys[i])
		out.conts = append(out.conts, src.conts[i].clone())
	}
	for ia < len(a.keys) && ib < len(b.keys) {
		ka, kb := a.keys[ia], b.keys[ib]
		switch {
		case ka < kb:
			appendClone(a, ia)
			ia++
		case ka > kb:
			appendClone(b, ib)
			ib++
		default:
			if c := orContainers(a.conts[ia], b.conts[ib]); c.cardinality() > 0 {
				out.keys = append(out.keys, ka)
				out.conts = append(out.conts, c)
			}
			ia++
			ib++
		}
	}
	for ; ia < len(a.keys); ia++ {
		appendClone(a, ia)
	}
	for ; ib < len(b.keys); ib++ {
		appendClone(b, ib)
	}
	return out
}

// Xor returns the symmetric difference of a and b.
func Xor(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	out.keys = make([]uint16, 0, len(a.keys)+len(b.keys))
	out.conts = make([]container, 0, len(a.keys)+len(b.keys))
	ia, ib := 0, 0
	appendClone := func(src *Bitmap, i int) {
		out.keys = append(out.keys, src.keys[i])
		out.conts = append(out.conts, src.conts[i].clone())
	}
	for ia < len(a.keys) && ib < len(b.keys) {
		ka, kb := a.keys[ia], b.keys[ib]
		switch {
		case ka < kb:
			appendClone(a, ia)
			ia++
		case ka > kb:
			appendClone(b, ib)
			ib++
		default:
			if c := xorContainers(a.conts[ia], b.conts[ib]); c.cardinality() > 0 {
				out.keys = append(out.keys, ka)
				out.conts = append(out.conts, c)
			}
			ia++
			ib++
		}
	}
	for ; ia < len(a.keys); ia++ {
		appendClone(a, ia)
	}
	for ; ib < len(b.keys); ib++ {
		appendClone(b, ib)
	}
	return out
}

// AndAll intersects zero or more bitmaps.
func AndAll(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	res := bs[0].Clone()
	for _, b := range bs[1:] {
		res = And(res, b)
	}
	return res
}

// OrAll unions zero or more bitmaps.
func OrAll(bs ...*Bitmap) *Bitmap {
	res := New()
	for _, b := range bs {
		res = Or(res, b)
	}
	return res
}

// XorAll folds symmetric difference over zero or more bitmaps.
func XorAll(bs ...*Bitmap) *Bitmap {
	res := New()
	for _, b := range bs {
		res = Xor(res, b)
	}
	return res
}
