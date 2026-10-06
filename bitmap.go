// Package bitmap implements a compressed bitmap over uint32 values.
//
// The value space is split into containers keyed by the high 16 bits.
// Each container is stored either as a sorted array (sparse) or a
// 65536-bit bitmap (dense); the switch threshold is configured at
// construction time. All mutating operations restore the canonical
// representation and keep the cached cardinality exact.
package bitmap

import "sort"

// DefaultArrayMaxSize is the default array/bitmap switch threshold.
const DefaultArrayMaxSize = 4096

// Bitmap is a set of uint32 values backed by per-key containers.
// keys is sorted ascending and aligned with cons. No container is empty
// and every container is in canonical form for b.threshold.
type Bitmap struct {
	threshold int
	keys      []uint16
	cons      []container
}

// New returns an empty Bitmap. arrayMaxSize is the maximum cardinality
// for which a container is kept as a sorted array; above it a bitmap is
// used. Values outside [1, 65536] are clamped.
func New(arrayMaxSize int) *Bitmap {
	if arrayMaxSize < 1 {
		arrayMaxSize = 1
	}
	if arrayMaxSize > maxContainerCard {
		arrayMaxSize = maxContainerCard
	}
	return &Bitmap{threshold: arrayMaxSize}
}

// Default returns an empty Bitmap with the default threshold.
func Default() *Bitmap { return New(DefaultArrayMaxSize) }

// Threshold returns the configured array/bitmap switch threshold.
func (b *Bitmap) Threshold() int { return b.threshold }

// findKey returns the index of key in b.keys and whether it was found.
func (b *Bitmap) findKey(key uint16) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	return i, i < len(b.keys) && b.keys[i] == key
}

// insertContainer inserts c for key at index i.
func (b *Bitmap) insertContainer(i int, key uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = key
	b.cons = append(b.cons, container{})
	copy(b.cons[i+1:], b.cons[i:])
	b.cons[i] = c
}

// removeContainer drops the container at index i.
func (b *Bitmap) removeContainer(i int) {
	b.keys = append(b.keys[:i], b.keys[i+1:]...)
	b.cons = append(b.cons[:i], b.cons[i+1:]...)
}

// Add inserts x, reporting whether the set changed.
func (b *Bitmap) Add(x uint32) bool {
	key, lo := uint16(x>>16), uint16(x)
	i, ok := b.findKey(key)
	if !ok {
		b.insertContainer(i, key, newArrayContainer([]uint16{lo}))
		return true
	}
	c := &b.cons[i]
	if !c.add(lo) {
		return false
	}
	c.normalize(b.threshold)
	return true
}

// Remove deletes x, reporting whether the set changed.
func (b *Bitmap) Remove(x uint32) bool {
	key, lo := uint16(x>>16), uint16(x)
	i, ok := b.findKey(key)
	if !ok {
		return false
	}
	c := &b.cons[i]
	if !c.remove(lo) {
		return false
	}
	if c.card == 0 {
		b.removeContainer(i)
		return true
	}
	c.normalize(b.threshold)
	return true
}

// Contains reports whether x is in the set.
func (b *Bitmap) Contains(x uint32) bool {
	i, ok := b.findKey(uint16(x >> 16))
	return ok && b.cons[i].contains(uint16(x))
}

// Cardinality returns the number of elements, summed from the
// per-container cached cardinalities.
func (b *Bitmap) Cardinality() int {
	n := 0
	for i := range b.cons {
		n += b.cons[i].card
	}
	return n
}

// IsEmpty reports whether the set is empty.
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// FlipRange toggles every value in [lo, hi). The range is clamped to
// the uint32 space; hi may be 1<<32 to include math.MaxUint32.
func (b *Bitmap) FlipRange(lo, hi uint64) {
	const space = uint64(1) << 32
	if hi > space {
		hi = space
	}
	if lo >= hi {
		return
	}
	keyLo, keyHi := uint32(lo>>16), uint32((hi-1)>>16)
	for k := keyLo; k <= keyHi; k++ {
		start, end := uint32(0), uint32(maxContainerCard)
		if k == keyLo {
			start = uint32(lo & 0xffff)
		}
		if k == keyHi {
			end = uint32((hi-1)&0xffff) + 1
		}
		b.flipContainerRange(uint16(k), start, end)
	}
}

// flipContainerRange toggles local offsets [start, end) in the
// container for key, creating or removing the container as needed.
func (b *Bitmap) flipContainerRange(key uint16, start, end uint32) {
	i, ok := b.findKey(key)
	if !ok {
		c := newArrayContainer(nil)
		c.flipRange(start, end)
		c.normalize(b.threshold)
		b.insertContainer(i, key, c)
		return
	}
	c := &b.cons[i]
	c.flipRange(start, end)
	if c.card == 0 {
		b.removeContainer(i)
		return
	}
	c.normalize(b.threshold)
}

// And returns the intersection of a and b, using a's threshold.
func (a *Bitmap) And(b *Bitmap) *Bitmap {
	out := New(a.threshold)
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] == b.keys[j]:
			if c := intersectContainers(&a.cons[i], &b.cons[j], a.threshold); c != nil {
				out.keys = append(out.keys, a.keys[i])
				out.cons = append(out.cons, *c)
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

// Or returns the union of a and b, using a's threshold.
func (a *Bitmap) Or(b *Bitmap) *Bitmap {
	out := New(a.threshold)
	i, j := 0, 0
	for i < len(a.keys) || j < len(b.keys) {
		switch {
		case j >= len(b.keys) || (i < len(a.keys) && a.keys[i] < b.keys[j]):
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, a.cons[i].clone())
			i++
		case i >= len(a.keys) || b.keys[j] < a.keys[i]:
			out.keys = append(out.keys, b.keys[j])
			out.cons = append(out.cons, b.cons[j].clone())
			j++
		default:
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, *unionContainers(&a.cons[i], &b.cons[j], a.threshold))
			i++
			j++
		}
	}
	return out
}

// AndNot returns the difference a \ b, using a's threshold.
func (a *Bitmap) AndNot(b *Bitmap) *Bitmap {
	out := New(a.threshold)
	i, j := 0, 0
	for i < len(a.keys) {
		if j >= len(b.keys) || a.keys[i] < b.keys[j] {
			out.keys = append(out.keys, a.keys[i])
			out.cons = append(out.cons, a.cons[i].clone())
			i++
			continue
		}
		if a.keys[i] == b.keys[j] {
			if c := andNotContainers(&a.cons[i], &b.cons[j], a.threshold); c != nil {
				out.keys = append(out.keys, a.keys[i])
				out.cons = append(out.cons, *c)
			}
			i++
			j++
			continue
		}
		j++
	}
	return out
}

// Equal reports whether a and b hold identical values in identical
// canonical representations.
func (a *Bitmap) Equal(b *Bitmap) bool {
	if a.threshold != b.threshold || len(a.keys) != len(b.keys) {
		return false
	}
	for i := range a.keys {
		if a.keys[i] != b.keys[i] || !a.cons[i].equal(&b.cons[i]) {
			return false
		}
	}
	return true
}

// ToArray returns all values in ascending order.
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	var lows []uint16
	for i, key := range b.keys {
		base := uint32(key) << 16
		lows = b.cons[i].appendTo(lows[:0])
		for _, v := range lows {
			out = append(out, base|uint32(v))
		}
	}
	return out
}
