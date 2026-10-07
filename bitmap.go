// Package bitmap implements a compressed 32-bit bitmap. Values are chunked
// by their high 16 bits; each chunk is stored as a sorted array while sparse
// or a 65536-bit bitmap while dense. The array/bitmap threshold is
// configurable, every mutating operation restores the canonical
// representation, and container cardinalities are cached incrementally.
package bitmap

import "sort"

// DefaultArrayMaxSize is the default sparse/dense threshold: chunks with at
// most this many values use a sorted array, denser chunks use a bitmap.
const DefaultArrayMaxSize = 4096

// Option configures a Bitmap at construction time.
type Option func(*Bitmap)

// WithArrayMaxSize sets the sparse/dense threshold. Values outside
// [1, 65536] are clamped.
func WithArrayMaxSize(n int) Option {
	return func(b *Bitmap) {
		if n < 1 {
			n = 1
		}
		if n > 1<<16 {
			n = 1 << 16
		}
		b.threshold = n
	}
}

// Bitmap is a set of uint32 values. The zero value is not ready for use;
// construct one with New.
type Bitmap struct {
	keys       []uint16 // high 16 bits, sorted, aligned with containers
	containers []container
	threshold  int
	card       int  // cached total cardinality
	cardValid  bool // whether card is up to date
}

// New returns an empty Bitmap configured by opts.
func New(opts ...Option) *Bitmap {
	b := &Bitmap{threshold: DefaultArrayMaxSize}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Bitmap) findKey(key uint16) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	return i, i < len(b.keys) && b.keys[i] == key
}

func (b *Bitmap) insertAt(i int, key uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = key
	b.containers = append(b.containers, nil)
	copy(b.containers[i+1:], b.containers[i:])
	b.containers[i] = c
}

func (b *Bitmap) removeAt(i int) {
	copy(b.keys[i:], b.keys[i+1:])
	b.keys = b.keys[:len(b.keys)-1]
	copy(b.containers[i:], b.containers[i+1:])
	b.containers = b.containers[:len(b.containers)-1]
}

// normalizeAt restores the canonical representation of container i and
// drops it if it became empty.
func (b *Bitmap) normalizeAt(i int) {
	c := normalize(b.containers[i], b.threshold)
	if c.cardinality() == 0 {
		b.removeAt(i)
		return
	}
	b.containers[i] = c
}

// Add inserts x, returning true if it was not already present.
func (b *Bitmap) Add(x uint32) bool {
	key, lo := uint16(x>>16), uint16(x)
	i, found := b.findKey(key)
	if !found {
		b.insertAt(i, key, &arrayContainer{values: []uint16{lo}})
		if b.cardValid {
			b.card++
		}
		return true
	}
	var added bool
	switch c := b.containers[i].(type) {
	case *arrayContainer:
		added = c.add(lo)
	case *bitmapContainer:
		added = c.add(lo)
	}
	if added {
		b.normalizeAt(i)
		if b.cardValid {
			b.card++
		}
	}
	return added
}

// Remove deletes x, returning true if it was present.
func (b *Bitmap) Remove(x uint32) bool {
	key, lo := uint16(x>>16), uint16(x)
	i, found := b.findKey(key)
	if !found {
		return false
	}
	var removed bool
	switch c := b.containers[i].(type) {
	case *arrayContainer:
		removed = c.remove(lo)
	case *bitmapContainer:
		removed = c.remove(lo)
	}
	if removed {
		b.normalizeAt(i)
		if b.cardValid {
			b.card--
		}
	}
	return removed
}

// Contains reports whether x is in the set.
func (b *Bitmap) Contains(x uint32) bool {
	i, found := b.findKey(uint16(x >> 16))
	return found && b.containers[i].contains(uint16(x))
}

// Cardinality returns the number of values in the set, using and refreshing
// the cached total.
func (b *Bitmap) Cardinality() int {
	if !b.cardValid {
		n := 0
		for _, c := range b.containers {
			n += c.cardinality()
		}
		b.card = n
		b.cardValid = true
	}
	return b.card
}

// IsEmpty reports whether the set is empty.
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// Clone returns a deep copy of b.
func (b *Bitmap) Clone() *Bitmap {
	out := &Bitmap{
		keys:       append([]uint16(nil), b.keys...),
		containers: make([]container, len(b.containers)),
		threshold:  b.threshold,
		card:       b.card,
		cardValid:  b.cardValid,
	}
	for i, c := range b.containers {
		out.containers[i] = c.clone()
	}
	return out
}

// Equals reports whether b and o contain the same values.
func (b *Bitmap) Equals(o *Bitmap) bool {
	if len(b.keys) != len(o.keys) {
		return false
	}
	for i := range b.keys {
		if b.keys[i] != o.keys[i] {
			return false
		}
		bc, oc := b.containers[i], o.containers[i]
		if bc.cardinality() != oc.cardinality() {
			return false
		}
		switch bt := bc.(type) {
		case *arrayContainer:
			ot, ok := oc.(*arrayContainer)
			if !ok {
				return false
			}
			for j, v := range bt.values {
				if ot.values[j] != v {
					return false
				}
			}
		case *bitmapContainer:
			ot, ok := oc.(*bitmapContainer)
			if !ok || bt.bits != ot.bits {
				return false
			}
		}
	}
	return true
}

// And returns the intersection of b and o.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	out := New(WithArrayMaxSize(b.threshold))
	i, j := 0, 0
	for i < len(b.keys) && j < len(o.keys) {
		switch {
		case b.keys[i] < o.keys[j]:
			i++
		case b.keys[i] > o.keys[j]:
			j++
		default:
			if c := normalize(andContainers(b.containers[i], o.containers[j]), b.threshold); c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.containers = append(out.containers, c)
			}
			i++
			j++
		}
	}
	return out
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	out := New(WithArrayMaxSize(b.threshold))
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			out.keys = append(out.keys, b.keys[i])
			out.containers = append(out.containers, b.containers[i].clone())
			i++
		case i >= len(b.keys) || o.keys[j] < b.keys[i]:
			out.keys = append(out.keys, o.keys[j])
			out.containers = append(out.containers, o.containers[j].clone())
			j++
		default:
			c := normalize(orContainers(b.containers[i], o.containers[j]), b.threshold)
			out.keys = append(out.keys, b.keys[i])
			out.containers = append(out.containers, c)
			i++
			j++
		}
	}
	return out
}

// AndNot returns the difference b \ o.
func (b *Bitmap) AndNot(o *Bitmap) *Bitmap {
	out := New(WithArrayMaxSize(b.threshold))
	for i, key := range b.keys {
		j, found := o.findKey(key)
		if !found {
			out.keys = append(out.keys, key)
			out.containers = append(out.containers, b.containers[i].clone())
			continue
		}
		if c := normalize(andNotContainers(b.containers[i], o.containers[j]), b.threshold); c.cardinality() > 0 {
			out.keys = append(out.keys, key)
			out.containers = append(out.containers, c)
		}
	}
	return out
}

// Flip toggles membership of every value in [lo, hi).
func (b *Bitmap) Flip(lo, hi uint64) {
	if lo >= hi {
		return
	}
	kLo := uint32(lo >> 16)
	kHi := uint32((hi - 1) >> 16)
	for k := kLo; k <= kHi; k++ {
		start := uint32(0)
		if k == kLo {
			start = uint32(uint16(lo))
		}
		end := uint32(1 << 16)
		if k == kHi {
			end = uint32(uint16(hi-1)) + 1
		}
		b.flipRange(uint16(k), start, end)
	}
	b.cardValid = false
}

// flipRange toggles bits [start, end) within the container for key.
func (b *Bitmap) flipRange(key uint16, start, end uint32) {
	i, found := b.findKey(key)
	var bc *bitmapContainer
	if found {
		bc = toBitmapContainer(b.containers[i])
	} else {
		bc = &bitmapContainer{}
	}
	for pos := start; pos < end; {
		wi := pos >> 6
		wordEnd := (wi + 1) << 6
		if wordEnd > end {
			wordEnd = end
		}
		mask := (^uint64(0) << (pos & 63)) & (^uint64(0) >> (63 - (wordEnd-1)&63))
		bc.bits[wi] ^= mask
		pos = wordEnd
	}
	bc.recomputeCard()
	nc := normalize(bc, b.threshold)
	switch {
	case nc.cardinality() == 0:
		if found {
			b.removeAt(i)
		}
	case found:
		b.containers[i] = nc
	default:
		b.insertAt(i, key, nc)
	}
}

// ToArray returns all values in ascending order.
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	for i, key := range b.keys {
		hi := uint32(key) << 16
		switch c := b.containers[i].(type) {
		case *arrayContainer:
			for _, v := range c.values {
				out = append(out, hi|uint32(v))
			}
		case *bitmapContainer:
			ac := c.toArray()
			for _, v := range ac.values {
				out = append(out, hi|uint32(v))
			}
		}
	}
	return out
}
