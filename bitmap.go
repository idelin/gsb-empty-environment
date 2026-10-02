// Package roaring implements a compressed bitmap index with three container
// encodings (array, bitmap, run-length), automatic container selection,
// fast boolean set algebra, versioned compact binary serialization and
// zero-copy / memory-mapped deserialization.
//
// Concurrency: a Bitmap is safe for concurrent reads and for concurrent
// boolean/serialization operations as long as no goroutine mutates it.
// Add/AddRange mutate in place and are NOT goroutine-safe; use Clone to
// obtain a private copy before mutating a shared bitmap.
package roaring

import "sort"

// bitmapEntry binds a high-16-bit key to its container.
// cow marks containers whose memory aliases an external (e.g. mmap'd)
// buffer; they are cloned lazily on first write.
type bitmapEntry struct {
	key uint16
	c   container
	cow bool
}

// Bitmap is a compressed set of uint32 values.
type Bitmap struct {
	entries []bitmapEntry // sorted by key
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

func (b *Bitmap) search(key uint16) int {
	return sort.Search(len(b.entries), func(i int) bool { return b.entries[i].key >= key })
}

// Add inserts id into the bitmap.
func (b *Bitmap) Add(id uint32) {
	hi, lo := uint16(id>>16), uint16(id)
	if n := len(b.entries); n > 0 {
		if last := &b.entries[n-1]; last.key == hi {
			if last.cow {
				last.c = last.c.clone()
				last.cow = false
			}
			last.c = containerAdd(last.c, lo)
			return
		}
		if b.entries[n-1].key < hi { // fast path: append at the end
			b.entries = append(b.entries, bitmapEntry{key: hi, c: &arrayContainer{vals: []uint16{lo}}})
			return
		}
	}
	i := b.search(hi)
	if i < len(b.entries) && b.entries[i].key == hi {
		e := &b.entries[i]
		if e.cow {
			e.c = e.c.clone()
			e.cow = false
		}
		e.c = containerAdd(e.c, lo)
		return
	}
	b.entries = append(b.entries, bitmapEntry{})
	copy(b.entries[i+1:], b.entries[i:])
	b.entries[i] = bitmapEntry{key: hi, c: &arrayContainer{vals: []uint16{lo}}}
}

// AddRange inserts every id in [start, end) into the bitmap.
// end may be as large as 1<<32.
func (b *Bitmap) AddRange(start uint32, end uint64) {
	if end > 1<<32 {
		end = 1 << 32
	}
	if uint64(start) >= end {
		return
	}
	hiStart := start >> 16
	hiEnd := uint32((end - 1) >> 16)
	for hi := hiStart; hi <= hiEnd; hi++ {
		lo, hiV := 0, containerCapacity-1
		if hi == hiStart {
			lo = int(start & 0xffff)
		}
		if hi == hiEnd {
			hiV = int((end - 1) & 0xffff)
		}
		b.addContainerRange(uint16(hi), lo, hiV)
	}
}

func (b *Bitmap) addContainerRange(key uint16, lo, hi int) {
	if n := len(b.entries); n == 0 || b.entries[n-1].key < key { // fast path: append
		rc := repair(&runContainer{iv: []runInterval{{start: uint16(lo), length: uint16(hi - lo)}}})
		b.entries = append(b.entries, bitmapEntry{key: key, c: rc})
		return
	}
	i := b.search(key)
	if i < len(b.entries) && b.entries[i].key == key {
		e := &b.entries[i]
		if e.cow {
			e.c = e.c.clone()
			e.cow = false
		}
		if rc, ok := e.c.(*runContainer); ok {
			e.c = repair(unionRuns(rc, &runContainer{iv: []runInterval{{start: uint16(lo), length: uint16(hi - lo)}}}))
			return
		}
		bc := e.c.toBitmap()
		setBitsRange(bc.words, lo, hi)
		bc.card = popcountWords(bc.words)
		e.c = repair(bc)
		return
	}
	rc := repair(&runContainer{iv: []runInterval{{start: uint16(lo), length: uint16(hi - lo)}}})
	b.entries = append(b.entries, bitmapEntry{})
	copy(b.entries[i+1:], b.entries[i:])
	b.entries[i] = bitmapEntry{key: key, c: rc}
}

// Contains reports whether id is present.
func (b *Bitmap) Contains(id uint32) bool {
	hi := uint16(id >> 16)
	i := b.search(hi)
	return i < len(b.entries) && b.entries[i].key == hi && b.entries[i].c.contains(uint16(id))
}

// Cardinality returns the number of values in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	for i := range b.entries {
		n += uint64(b.entries[i].c.cardinality())
	}
	return n
}

// IsEmpty reports whether the bitmap has no values.
func (b *Bitmap) IsEmpty() bool { return len(b.entries) == 0 }

// ToArray returns all values in ascending order.
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	for i := range b.entries {
		ac := b.entries[i].c.toArray()
		base := uint32(b.entries[i].key) << 16
		for _, v := range ac.vals {
			out = append(out, base|uint32(v))
		}
	}
	return out
}

// Clone returns a deep copy that shares no memory with b.
func (b *Bitmap) Clone() *Bitmap {
	out := &Bitmap{entries: make([]bitmapEntry, len(b.entries))}
	for i, e := range b.entries {
		out.entries[i] = bitmapEntry{key: e.key, c: e.c.clone()}
	}
	return out
}

// Equals reports whether b and o contain exactly the same values.
func (b *Bitmap) Equals(o *Bitmap) bool {
	if len(b.entries) != len(o.entries) {
		return false
	}
	for i := range b.entries {
		if b.entries[i].key != o.entries[i].key {
			return false
		}
		if containerXor(b.entries[i].c, o.entries[i].c) != nil {
			return false
		}
	}
	return true
}

// Stats summarizes container usage of the bitmap.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
	SerializedBytes  int
}

// Stats computes container statistics for the bitmap.
func (b *Bitmap) Stats() Stats {
	s := Stats{Containers: len(b.entries), SerializedBytes: b.SerializedSize()}
	for i := range b.entries {
		s.Cardinality += uint64(b.entries[i].c.cardinality())
		switch b.entries[i].c.typ() {
		case containerTypeArray:
			s.ArrayContainers++
		case containerTypeBitmap:
			s.BitmapContainers++
		case containerTypeRun:
			s.RunContainers++
		}
	}
	return s
}

// containerAdd inserts v into c, promoting encodings when needed.
func containerAdd(c container, v uint16) container {
	switch t := c.(type) {
	case *arrayContainer:
		i := searchUint16s(t.vals, v)
		if i < len(t.vals) && t.vals[i] == v {
			return t
		}
		t.vals = append(t.vals, 0)
		copy(t.vals[i+1:], t.vals[i:])
		t.vals[i] = v
		if len(t.vals) > arrayMaxCardinality {
			return t.toBitmap()
		}
		return t
	case *bitmapContainer:
		w := &t.words[v>>6]
		mask := uint64(1) << (v & 63)
		if *w&mask == 0 {
			*w |= mask
			t.card++
		}
		return t
	case *runContainer:
		return unionRuns(t, &runContainer{iv: []runInterval{{start: v, length: 0}}})
	}
	return c
}
