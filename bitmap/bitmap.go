package bitmap

import "sort"

// Bitmap is a compressed 64-bit-id set. Ids are split by their high 16 bits
// into per-key containers (Array / Bitmap / RLE), chosen automatically.
//
// A Bitmap is not safe for concurrent mutation; use SafeBitmap or
// synchronize externally. Read-only operations (And/Or/Xor/Contains/...)
// do not mutate their receivers and may run concurrently.
type Bitmap struct {
	keys  []uint64 // sorted high 48 bits (id >> 16)
	conts []container
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

// locate returns the index of key k, or its insertion point.
func (b *Bitmap) locate(k uint64) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= k })
	if i < len(b.keys) && b.keys[i] == k {
		return i, true
	}
	return i, false
}

func (b *Bitmap) insertAt(i int, k uint64, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = k
	b.conts = append(b.conts, nil)
	copy(b.conts[i+1:], b.conts[i:])
	b.conts[i] = c
}

// Add inserts a single id.
func (b *Bitmap) Add(id uint64) {
	k, v := id>>16, uint16(id)
	i, found := b.locate(k)
	if !found {
		b.insertAt(i, k, &arrayContainer{data: []uint16{v}})
		return
	}
	b.conts[i] = b.conts[i].add(v)
}

// AddRange inserts every id in [lo, hi). Dense ranges are stored as RLE
// runs, so a range of any size costs O(1) memory per covered container.
func (b *Bitmap) AddRange(lo, hi uint64) {
	if lo >= hi {
		return
	}
	kLo, kHi := lo>>16, (hi-1)>>16
	for k := kLo; k <= kHi; k++ {
		var segLo, segHi uint16
		if k == kLo {
			segLo = uint16(lo)
		}
		if k == kHi {
			segHi = uint16(hi - 1)
		} else {
			segHi = 0xffff
		}
		seg := &runContainer{runs: []interval{{segLo, segHi}}}
		i, found := b.locate(k)
		if !found {
			b.insertAt(i, k, seg)
		} else {
			b.conts[i] = optimize(orContainers(b.conts[i], seg))
		}
	}
}

// Contains reports whether id is present.
func (b *Bitmap) Contains(id uint64) bool {
	i, found := b.locate(id >> 16)
	return found && b.conts[i].contains(uint16(id))
}

// Cardinality returns the number of ids in the set.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	for _, c := range b.conts {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the set is empty.
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// Optimize rewrites every container into its most compact form.
func (b *Bitmap) Optimize() {
	for i, c := range b.conts {
		b.conts[i] = optimize(c)
	}
}

// Clone returns a deep copy.
func (b *Bitmap) Clone() *Bitmap {
	out := &Bitmap{keys: make([]uint64, len(b.keys)), conts: make([]container, len(b.conts))}
	copy(out.keys, b.keys)
	for i, c := range b.conts {
		out.conts[i] = c.clone()
	}
	return out
}

// And returns the intersection of b and o.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	n := len(b.keys)
	if len(o.keys) < n {
		n = len(o.keys)
	}
	out := &Bitmap{keys: make([]uint64, 0, n), conts: make([]container, 0, n)}
	i, j := 0, 0
	for i < len(b.keys) && j < len(o.keys) {
		switch {
		case b.keys[i] < o.keys[j]:
			i++
		case b.keys[i] > o.keys[j]:
			j++
		default:
			if c := andContainers(b.conts[i], o.conts[j]); c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.conts = append(out.conts, c)
			}
			i++
			j++
		}
	}
	return out
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	n := len(b.keys) + len(o.keys)
	out := &Bitmap{keys: make([]uint64, 0, n), conts: make([]container, 0, n)}
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, b.conts[i].clone())
			i++
		case i >= len(b.keys) || o.keys[j] < b.keys[i]:
			out.keys = append(out.keys, o.keys[j])
			out.conts = append(out.conts, o.conts[j].clone())
			j++
		default:
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, orContainers(b.conts[i], o.conts[j]))
			i++
			j++
		}
	}
	return out
}

// Xor returns the symmetric difference of b and o.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	n := len(b.keys) + len(o.keys)
	out := &Bitmap{keys: make([]uint64, 0, n), conts: make([]container, 0, n)}
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			out.keys = append(out.keys, b.keys[i])
			out.conts = append(out.conts, b.conts[i].clone())
			i++
		case i >= len(b.keys) || o.keys[j] < b.keys[i]:
			out.keys = append(out.keys, o.keys[j])
			out.conts = append(out.conts, o.conts[j].clone())
			j++
		default:
			if c := xorContainers(b.conts[i], o.conts[j]); c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.conts = append(out.conts, c)
			}
			i++
			j++
		}
	}
	return out
}

// AndMany intersects all given bitmaps.
func AndMany(bs ...*Bitmap) *Bitmap {
	if len(bs) == 0 {
		return New()
	}
	res := bs[0].Clone()
	for _, o := range bs[1:] {
		res = res.And(o)
	}
	return res
}

// OrMany unions all given bitmaps.
func OrMany(bs ...*Bitmap) *Bitmap {
	res := New()
	for _, o := range bs {
		res = res.Or(o)
	}
	return res
}

// XorMany folds symmetric difference over all given bitmaps.
func XorMany(bs ...*Bitmap) *Bitmap {
	res := New()
	for _, o := range bs {
		res = res.Xor(o)
	}
	return res
}

// ForEach visits every id in ascending order.
func (b *Bitmap) ForEach(f func(uint64)) {
	for i, k := range b.keys {
		base := k << 16
		b.conts[i].foreach(func(v uint16) { f(base | uint64(v)) })
	}
}

// Equal reports whether b and o contain exactly the same ids.
func (b *Bitmap) Equal(o *Bitmap) bool {
	if len(b.keys) != len(o.keys) || b.Cardinality() != o.Cardinality() {
		return false
	}
	for i := range b.keys {
		if b.keys[i] != o.keys[i] {
			return false
		}
		aa, bb := b.conts[i].toArray().data, o.conts[i].toArray().data
		if len(aa) != len(bb) {
			return false
		}
		for j := range aa {
			if aa[j] != bb[j] {
				return false
			}
		}
	}
	return true
}

// Checksum returns a full-content FNV-1a hash (structure + values).
func (b *Bitmap) Checksum() uint64 {
	h := uint64(fnvOffset64)
	for i, k := range b.keys {
		h = fnv64(h, k)
		h = fnvByte(h, b.conts[i].typeTag())
		h = b.conts[i].hash(h)
	}
	return h
}

// StructureChecksum is a cheap O(containers) hash of shape and sizes,
// used by the deterministic simulation loop.
func (b *Bitmap) StructureChecksum() uint64 {
	h := uint64(fnvOffset64)
	for i, k := range b.keys {
		h = fnv64(h, k)
		h = fnvByte(h, b.conts[i].typeTag())
		h = fnv64(h, uint64(b.conts[i].cardinality()))
	}
	return h
}

// Stats summarizes container usage.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
	SizeInBytes      int
}

// Stats returns per-container-type counters for b.
func (b *Bitmap) Stats() Stats {
	s := Stats{Containers: len(b.conts)}
	for _, c := range b.conts {
		s.Cardinality += uint64(c.cardinality())
		s.SizeInBytes += c.sizeInBytes()
		switch c.(type) {
		case *arrayContainer:
			s.ArrayContainers++
		case *bitmapContainer:
			s.BitmapContainers++
		case *runContainer:
			s.RunContainers++
		}
	}
	return s
}
