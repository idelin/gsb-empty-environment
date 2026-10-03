package roaring

import "fmt"

// Bitmap is a compressed bitmap over the uint32 key space. Keys are split
// into high 16 bits (index into the container list) and low 16 bits
// (stored inside a container).
type Bitmap struct {
	keys  []uint16
	conts []container
}

// New returns an empty bitmap.
func New() *Bitmap { return &Bitmap{} }

// search returns the index of key, or -insertionPoint-1 when absent.
func (b *Bitmap) search(key uint16) int {
	lo, hi := 0, len(b.keys)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if b.keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(b.keys) && b.keys[lo] == key {
		return lo
	}
	return -lo - 1
}

func (b *Bitmap) insertAt(idx int, key uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[idx+1:], b.keys[idx:])
	b.keys[idx] = key
	b.conts = append(b.conts, nil)
	copy(b.conts[idx+1:], b.conts[idx:])
	b.conts[idx] = c
}

func (b *Bitmap) removeAt(idx int) {
	b.keys = append(b.keys[:idx], b.keys[idx+1:]...)
	b.conts = append(b.conts[:idx], b.conts[idx+1:]...)
}

// Add inserts a single ID.
func (b *Bitmap) Add(v uint32) {
	hi := uint16(v >> 16)
	lo := uint16(v)
	idx := b.search(hi)
	if idx >= 0 {
		c, _ := b.conts[idx].add(lo)
		b.conts[idx] = c
		return
	}
	b.insertAt(-idx-1, hi, &arrayContainer{content: []uint16{lo}})
}

// AddRange inserts all IDs in [start, end). end is clamped to 2^32.
func (b *Bitmap) AddRange(start, end uint64) {
	if end > MaxValue {
		end = MaxValue
	}
	if start >= end {
		return
	}
	hiStart := uint32(start >> 16)
	hiEnd := uint32((end - 1) >> 16)
	for h := hiStart; h <= hiEnd; h++ {
		lo := uint16(0)
		hi := uint16(0xFFFF)
		if h == hiStart {
			lo = uint16(start)
		}
		if h == hiEnd {
			hi = uint16(end - 1)
		}
		key := uint16(h)
		idx := b.search(key)
		if idx < 0 {
			idx = -idx - 1
			b.insertAt(idx, key, newContainerForRange(lo, hi))
			b.conts[idx] = b.conts[idx].optimize()
			continue
		}
		b.conts[idx] = b.conts[idx].addRange(lo, hi).optimize()
	}
}

// Remove deletes a single ID.
func (b *Bitmap) Remove(v uint32) {
	hi := uint16(v >> 16)
	lo := uint16(v)
	idx := b.search(hi)
	if idx < 0 {
		return
	}
	c, _ := b.conts[idx].remove(lo)
	if c.cardinality() == 0 {
		b.removeAt(idx)
		return
	}
	b.conts[idx] = c
}

// RemoveRange deletes all IDs in [start, end). end is clamped to 2^32.
func (b *Bitmap) RemoveRange(start, end uint64) {
	if end > MaxValue {
		end = MaxValue
	}
	if start >= end {
		return
	}
	hiStart := uint32(start >> 16)
	hiEnd := uint32((end - 1) >> 16)
	for h := hiStart; h <= hiEnd; h++ {
		key := uint16(h)
		idx := b.search(key)
		if idx < 0 {
			continue
		}
		lo := uint16(0)
		hi := uint16(0xFFFF)
		if h == hiStart {
			lo = uint16(start)
		}
		if h == hiEnd {
			hi = uint16(end - 1)
		}
		c := b.conts[idx].removeRange(lo, hi)
		if c.cardinality() == 0 {
			b.removeAt(idx)
			continue
		}
		b.conts[idx] = c.optimize()
	}
}

// Contains reports whether v is present.
func (b *Bitmap) Contains(v uint32) bool {
	idx := b.search(uint16(v >> 16))
	if idx < 0 {
		return false
	}
	return b.conts[idx].contains(uint16(v))
}

// Cardinality returns the number of IDs in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	for _, c := range b.conts {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the bitmap has no IDs.
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// Containers returns the number of containers.
func (b *Bitmap) Containers() int { return len(b.keys) }

// ForEach iterates all IDs in ascending order.
func (b *Bitmap) ForEach(fn func(v uint32)) {
	for i, k := range b.keys {
		hi := uint32(k) << 16
		b.conts[i].forEach(func(lo uint16) {
			fn(hi | uint32(lo))
		})
	}
}

// ToArray returns all IDs in ascending order.
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	b.ForEach(func(v uint32) { out = append(out, v) })
	return out
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

// Equals reports whether both bitmaps contain the same IDs.
func (b *Bitmap) Equals(o *Bitmap) bool {
	return b.AndNot(o).IsEmpty() && o.AndNot(b).IsEmpty()
}

// Optimize converts every container to its most compact representation.
func (b *Bitmap) Optimize() {
	for i, c := range b.conts {
		b.conts[i] = c.optimize()
	}
}

// And returns the intersection of two bitmaps.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	out := &Bitmap{
		keys:  make([]uint16, 0, min(len(b.keys), len(o.keys))),
		conts: make([]container, 0, min(len(b.keys), len(o.keys))),
	}
	i, j := 0, 0
	for i < len(b.keys) && j < len(o.keys) {
		switch {
		case b.keys[i] == o.keys[j]:
			c := intersect(b.conts[i], o.conts[j])
			if c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.conts = append(out.conts, c.optimize())
			}
			i++
			j++
		case b.keys[i] < o.keys[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// Or returns the union of two bitmaps.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	out := &Bitmap{
		keys:  make([]uint16, 0, len(b.keys)+len(o.keys)),
		conts: make([]container, 0, len(b.keys)+len(o.keys)),
	}
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case i < len(b.keys) && (j >= len(o.keys) || b.keys[i] < o.keys[j]):
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, b.conts[i].clone())
			i++
		case j < len(o.keys) && (i >= len(b.keys) || o.keys[j] < b.keys[i]):
			out.keys = append(out.keys, o.keys[j])
			out.conts = append(out.conts, o.conts[j].clone())
			j++
		default:
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, union(b.conts[i], o.conts[j]).optimize())
			i++
			j++
		}
	}
	return out
}

// Xor returns the symmetric difference of two bitmaps.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	out := &Bitmap{
		keys:  make([]uint16, 0, len(b.keys)+len(o.keys)),
		conts: make([]container, 0, len(b.keys)+len(o.keys)),
	}
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case i < len(b.keys) && (j >= len(o.keys) || b.keys[i] < o.keys[j]):
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, b.conts[i].clone())
			i++
		case j < len(o.keys) && (i >= len(b.keys) || o.keys[j] < b.keys[i]):
			out.keys = append(out.keys, o.keys[j])
			out.conts = append(out.conts, o.conts[j].clone())
			j++
		default:
			c := xorContainers(b.conts[i], o.conts[j])
			if c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.conts = append(out.conts, c.optimize())
			}
			i++
			j++
		}
	}
	return out
}

// AndNot returns the difference b \ o.
func (b *Bitmap) AndNot(o *Bitmap) *Bitmap {
	out := &Bitmap{
		keys:  make([]uint16, 0, len(b.keys)),
		conts: make([]container, 0, len(b.keys)),
	}
	i, j := 0, 0
	for i < len(b.keys) {
		if j >= len(o.keys) || b.keys[i] < o.keys[j] {
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, b.conts[i].clone())
			i++
			continue
		}
		if o.keys[j] < b.keys[i] {
			j++
			continue
		}
		c := diff(b.conts[i], o.conts[j])
		if c.cardinality() > 0 {
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, c.optimize())
		}
		i++
		j++
	}
	return out
}

// AndAll intersects a batch of bitmaps.
func AndAll(bitmaps ...*Bitmap) *Bitmap {
	if len(bitmaps) == 0 {
		return New()
	}
	acc := bitmaps[0].Clone()
	for _, bm := range bitmaps[1:] {
		acc = acc.And(bm)
	}
	return acc
}

// OrAll unions a batch of bitmaps.
func OrAll(bitmaps ...*Bitmap) *Bitmap {
	acc := New()
	for _, bm := range bitmaps {
		acc = acc.Or(bm)
	}
	return acc
}

// XorAll computes the symmetric difference of a batch of bitmaps.
func XorAll(bitmaps ...*Bitmap) *Bitmap {
	acc := New()
	for _, bm := range bitmaps {
		acc = acc.Xor(bm)
	}
	return acc
}

// Stats summarizes the container composition of a bitmap.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
	SizeInBytes      int
}

// Stats returns container composition statistics.
func (b *Bitmap) Stats() Stats {
	s := Stats{Containers: len(b.conts)}
	for _, c := range b.conts {
		s.SizeInBytes += c.sizeInBytes()
		s.Cardinality += uint64(c.cardinality())
		switch c.ctype() {
		case typeArray:
			s.ArrayContainers++
		case typeBitmap:
			s.BitmapContainers++
		case typeRun:
			s.RunContainers++
		}
	}
	return s
}

// Validate checks structural invariants of the bitmap and its containers.
func (b *Bitmap) Validate() error {
	for i := 0; i < len(b.keys); i++ {
		if i > 0 && b.keys[i] <= b.keys[i-1] {
			return fmt.Errorf("container keys not strictly sorted at %d", i)
		}
		c := b.conts[i]
		if c.cardinality() == 0 {
			return fmt.Errorf("empty container at key %d", b.keys[i])
		}
		if err := c.validate(); err != nil {
			return fmt.Errorf("container key %d: %w", b.keys[i], err)
		}
	}
	return nil
}

// checkOptimal verifies every container uses the most compact representation.
func (b *Bitmap) checkOptimal() error {
	for i, c := range b.conts {
		want := pickContainerType(c.cardinality(), c.numRuns())
		if want != c.ctype() {
			return fmt.Errorf("container key %d: type %s, want %s", b.keys[i], c.ctype(), want)
		}
	}
	return nil
}

// DebugCorrupt flips bookkeeping inside the first container without
// updating dependent state. It exists solely to exercise failure
// detection and reproduction tooling; never use it in production.
func (b *Bitmap) DebugCorrupt() {
	if len(b.conts) == 0 {
		return
	}
	switch c := b.conts[0].(type) {
	case *bitmapContainer:
		c.bitmap[0] ^= 1
	case *arrayContainer:
		c.content = c.content[:len(c.content)-1]
	case *runContainer:
		c.runs = c.runs[:len(c.runs)-1]
	}
}
