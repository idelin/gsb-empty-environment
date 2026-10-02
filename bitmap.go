package roaring

import (
	"sort"
	"sync"
	"sync/atomic"
)

var bitmapSeq uint64

// Bitmap is a compressed 64-bit bitmap. IDs are split into a high 48-bit
// key and a low 16-bit value; values sharing a key live in one container
// whose representation (array / bitmap / RLE) is chosen automatically.
//
// All methods are safe for concurrent use.
type Bitmap struct {
	id   uint64
	mu   sync.RWMutex
	keys []uint64 // sorted high 48 bits
	cs   []container
}

// New returns an empty Bitmap.
func New() *Bitmap {
	return &Bitmap{id: atomic.AddUint64(&bitmapSeq, 1)}
}

func (b *Bitmap) search(key uint64) int {
	return sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
}

// Add inserts a single ID.
func (b *Bitmap) Add(id uint64) {
	key := id >> 16
	lo := uint16(id)
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.search(key)
	if i < len(b.keys) && b.keys[i] == key {
		b.cs[i] = b.cs[i].add(lo)
		return
	}
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = key
	b.cs = append(b.cs, nil)
	copy(b.cs[i+1:], b.cs[i:])
	b.cs[i] = (&arrayContainer{}).add(lo)
}

// AddRange inserts all IDs in [start, end). Contiguous blocks are stored
// as RLE containers, so huge dense ranges cost O(range/65536) containers.
func (b *Bitmap) AddRange(start, end uint64) {
	if end <= start {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	last := end - 1
	k0, k1 := start>>16, last>>16
	for key := k0; ; key++ {
		var lo, hi uint16 = 0, 0xffff
		if key == k0 {
			lo = uint16(start)
		}
		if key == k1 {
			hi = uint16(last)
		}
		i := b.search(key)
		if i < len(b.keys) && b.keys[i] == key {
			b.cs[i] = addRangeToContainer(b.cs[i], lo, hi)
		} else {
			b.keys = append(b.keys, 0)
			copy(b.keys[i+1:], b.keys[i:])
			b.keys[i] = key
			b.cs = append(b.cs, nil)
			copy(b.cs[i+1:], b.cs[i:])
			b.cs[i] = newRangeContainer(lo, hi)
		}
		if key == k1 {
			break
		}
	}
}

// Contains reports whether id is present.
func (b *Bitmap) Contains(id uint64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	key := id >> 16
	i := b.search(key)
	return i < len(b.keys) && b.keys[i] == key && b.cs[i].contains(uint16(id))
}

// Cardinality returns the number of IDs in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var n uint64
	for _, c := range b.cs {
		n += uint64(c.cardinality())
	}
	return n
}

// Optimize re-encodes every container into its most compact form.
func (b *Bitmap) Optimize() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, c := range b.cs {
		b.cs[i] = optimize(c)
	}
}

// Clone returns a deep copy.
func (b *Bitmap) Clone() *Bitmap {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := New()
	out.keys = append(out.keys, b.keys...)
	out.cs = make([]container, len(b.cs))
	for i, c := range b.cs {
		out.cs[i] = c.clone()
	}
	return out
}

// Equal reports whether both bitmaps contain exactly the same IDs.
func (b *Bitmap) Equal(o *Bitmap) bool {
	return b.Xor(o).Cardinality() == 0
}

// Stats describes the container mix of the bitmap.
type Stats struct {
	Containers  int
	Array       int
	Bitmap      int
	Run         int
	Bytes       int
	Cardinality uint64
}

// Stats returns container statistics.
func (b *Bitmap) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var s Stats
	s.Containers = len(b.cs)
	for _, c := range b.cs {
		s.Bytes += c.sizeBytes()
		s.Cardinality += uint64(c.cardinality())
		switch c.(type) {
		case *arrayContainer:
			s.Array++
		case *bitmapContainer:
			s.Bitmap++
		case *runContainer:
			s.Run++
		}
	}
	return s
}

// rlockPair read-locks two bitmaps in a deterministic order to avoid
// deadlock when goroutines combine the same pair in opposite orders.
func rlockPair(a, b *Bitmap) func() {
	if a == b {
		a.mu.RLock()
		return a.mu.RUnlock
	}
	if a.id < b.id {
		a.mu.RLock()
		b.mu.RLock()
		return func() { b.mu.RUnlock(); a.mu.RUnlock() }
	}
	b.mu.RLock()
	a.mu.RLock()
	return func() { a.mu.RUnlock(); b.mu.RUnlock() }
}

func (b *Bitmap) appendContainer(key uint64, c container) {
	if c.cardinality() == 0 {
		return
	}
	b.keys = append(b.keys, key)
	b.cs = append(b.cs, c)
}

// And returns the intersection of b and o.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	unlock := rlockPair(b, o)
	defer unlock()
	out := New()
	capHint := len(b.keys)
	if len(o.keys) < capHint {
		capHint = len(o.keys)
	}
	out.keys = make([]uint64, 0, capHint)
	out.cs = make([]container, 0, capHint)
	i, j := 0, 0
	for i < len(b.keys) && j < len(o.keys) {
		switch {
		case b.keys[i] < o.keys[j]:
			i++
		case b.keys[i] > o.keys[j]:
			j++
		default:
			out.appendContainer(b.keys[i], andContainers(b.cs[i], o.cs[j]))
			i++
			j++
		}
	}
	return out
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	unlock := rlockPair(b, o)
	defer unlock()
	out := New()
	out.keys = make([]uint64, 0, len(b.keys)+len(o.keys))
	out.cs = make([]container, 0, len(b.keys)+len(o.keys))
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			out.appendContainer(b.keys[i], b.cs[i].clone())
			i++
		case i >= len(b.keys) || o.keys[j] < b.keys[i]:
			out.appendContainer(o.keys[j], o.cs[j].clone())
			j++
		default:
			out.appendContainer(b.keys[i], orContainers(b.cs[i], o.cs[j]))
			i++
			j++
		}
	}
	return out
}

// Xor returns the symmetric difference of b and o.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	unlock := rlockPair(b, o)
	defer unlock()
	out := New()
	out.keys = make([]uint64, 0, len(b.keys)+len(o.keys))
	out.cs = make([]container, 0, len(b.keys)+len(o.keys))
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			out.appendContainer(b.keys[i], b.cs[i].clone())
			i++
		case i >= len(b.keys) || o.keys[j] < b.keys[i]:
			out.appendContainer(o.keys[j], o.cs[j].clone())
			j++
		default:
			out.appendContainer(b.keys[i], xorContainers(b.cs[i], o.cs[j]))
			i++
			j++
		}
	}
	return out
}

// AndBatch intersects all given bitmaps.
func AndBatch(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	acc := bs[0].Clone()
	for _, b := range bs[1:] {
		acc = acc.And(b)
	}
	return acc
}

// OrBatch unions all given bitmaps.
func OrBatch(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	acc := bs[0].Clone()
	for _, b := range bs[1:] {
		acc = acc.Or(b)
	}
	return acc
}

// XorBatch folds all given bitmaps with XOR.
func XorBatch(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	acc := bs[0].Clone()
	for _, b := range bs[1:] {
		acc = acc.Xor(b)
	}
	return acc
}
