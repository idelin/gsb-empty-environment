package roaring

import "sort"

// Config tunes representation switching. The zero value selects defaults.
type Config struct {
	// ArrayMax: an array container is legal while card <= ArrayMax.
	ArrayMax int
	// BitmapMin: a bitmap container is legal while card >= BitmapMin.
	// Must be < ArrayMax; the gap is the hysteresis band.
	BitmapMin int
	// HysteresisBytes: an opportunistic switch must save at least this
	// many bytes, preventing run/array flip-flopping.
	HysteresisBytes int
}

// DefaultConfig returns the thresholds used when Config is zero.
func DefaultConfig() Config {
	return Config{ArrayMax: 4096, BitmapMin: 3584, HysteresisBytes: 64}
}

func (c Config) withDefaults() Config {
	if c.ArrayMax <= 0 {
		c.ArrayMax = 4096
	}
	if c.BitmapMin <= 0 {
		c.BitmapMin = c.ArrayMax - c.ArrayMax/8
	}
	if c.BitmapMin >= c.ArrayMax {
		c.BitmapMin = c.ArrayMax - 1
	}
	if c.HysteresisBytes <= 0 {
		c.HysteresisBytes = 64
	}
	return c
}

// Stats reports real encoded sizes per representation.
type Stats struct {
	Buckets       int
	Cardinality   uint64
	ArrayBuckets  int
	BitmapBuckets int
	RunBuckets    int
	ArrayBytes    uint64
	BitmapBytes   uint64
	RunBytes      uint64
	TotalBytes    uint64 // sum of container payloads
	Conversions   int    // representation switches since creation
}

// Set is a compressed set of uint32. Values are split into a high-16-bit
// bucket directory and per-bucket compressed containers.
type Set struct {
	cfg         Config
	buckets     map[uint16]*container
	card        uint64
	conversions int
}

func New(cfg Config) *Set {
	return &Set{cfg: cfg.withDefaults(), buckets: make(map[uint16]*container)}
}

func (s *Set) Add(v uint32) {
	hi, lo := uint16(v>>16), uint16(v)
	c := s.buckets[hi]
	if c == nil {
		c = &container{typ: cArray}
		s.buckets[hi] = c
	}
	if c.add(lo) {
		s.card++
		s.normalizeBucket(hi)
	}
}

func (s *Set) Remove(v uint32) {
	hi, lo := uint16(v>>16), uint16(v)
	c := s.buckets[hi]
	if c == nil {
		return
	}
	if c.remove(lo) {
		s.card--
		s.normalizeBucket(hi)
	}
}

func (s *Set) normalizeBucket(hi uint16) {
	c := s.buckets[hi]
	if c.card == 0 {
		delete(s.buckets, hi)
		return
	}
	nc := normalize(c, s.cfg)
	if nc.typ != c.typ {
		s.conversions++
	}
	s.buckets[hi] = nc
}

func (s *Set) Contains(v uint32) bool {
	c := s.buckets[uint16(v>>16)]
	return c != nil && c.contains(uint16(v))
}

func (s *Set) Cardinality() uint64 { return s.card }

// Bytes returns the total encoded payload bytes across all buckets.
func (s *Set) Bytes() uint64 { return s.Stats().TotalBytes }

func (s *Set) Stats() Stats {
	st := Stats{Buckets: len(s.buckets), Cardinality: s.card, Conversions: s.conversions}
	for _, c := range s.buckets {
		switch c.typ {
		case cArray:
			st.ArrayBuckets++
			st.ArrayBytes += uint64(c.byteSize())
		case cBitmap:
			st.BitmapBuckets++
			st.BitmapBytes += uint64(c.byteSize())
		case cRun:
			st.RunBuckets++
			st.RunBytes += uint64(c.byteSize())
		}
		st.TotalBytes += uint64(c.byteSize())
	}
	return st
}

// Items returns all values in ascending order.
func (s *Set) Items() []uint32 {
	out := make([]uint32, 0, s.card)
	keys := make([]uint16, 0, len(s.buckets))
	for hi := range s.buckets {
		keys = append(keys, hi)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, hi := range keys {
		base := uint32(hi) << 16
		s.buckets[hi].foreach(func(lo uint16) {
			out = append(out, base|uint32(lo))
		})
	}
	return out
}

func (s *Set) bucketType(hi uint16) (ctype, bool) {
	c, ok := s.buckets[hi]
	if !ok {
		return 0, false
	}
	return c.typ, true
}

// ---- set operations, correct across all representation combinations ----

func (s *Set) Or(o *Set) *Set     { return s.binaryOp(o, opOr) }
func (s *Set) And(o *Set) *Set    { return s.binaryOp(o, opAnd) }
func (s *Set) AndNot(o *Set) *Set { return s.binaryOp(o, opAndNot) }
func (s *Set) Xor(o *Set) *Set    { return s.binaryOp(o, opXor) }

type opKind uint8

const (
	opOr opKind = iota
	opAnd
	opAndNot
	opXor
)

type combiner func(a, b *container) *container

func (s *Set) binaryOp(o *Set, op opKind) *Set {
	var combine combiner
	keepA, keepB := false, false // clone single-sided buckets into result
	switch op {
	case opOr:
		combine, keepA, keepB = combineOr, true, true
	case opAnd:
		combine = combineAnd
	case opAndNot:
		combine, keepA = combineAndNot, true
	case opXor:
		combine, keepA, keepB = combineXor, true, true
	}
	res := New(s.cfg)
	keys := make(map[uint16]struct{}, len(s.buckets))
	for hi := range s.buckets {
		keys[hi] = struct{}{}
	}
	for hi := range o.buckets {
		keys[hi] = struct{}{}
	}
	for hi := range keys {
		a, b := s.buckets[hi], o.buckets[hi]
		var nc *container
		switch {
		case a != nil && b != nil:
			nc = combine(a, b)
		case a != nil:
			if keepA {
				nc = a.clone()
			}
		case b != nil:
			if keepB {
				nc = b.clone()
			}
		}
		if nc == nil || nc.card == 0 {
			continue
		}
		nc = normalize(nc, s.cfg)
		res.buckets[hi] = nc
		res.card += uint64(nc.card)
	}
	return res
}

func combineOr(a, b *container) *container {
	if a.typ == cBitmap || b.typ == cBitmap {
		res := &container{typ: cBitmap, bmp: make([]uint64, bitmapWords)}
		a.foreach(func(v uint16) { res.bmp[v>>6] |= uint64(1) << (v & 63) })
		b.foreach(func(v uint16) { res.bmp[v>>6] |= uint64(1) << (v & 63) })
		res.card = bitmapCardinality(res.bmp)
		return res
	}
	ia, ib := a.iter(), b.iter()
	res := &container{typ: cArray}
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka || okb {
		switch {
		case oka && (!okb || va < vb):
			res.arr = append(res.arr, va)
			va, oka = ia.next()
		case okb && (!oka || vb < va):
			res.arr = append(res.arr, vb)
			vb, okb = ib.next()
		default:
			res.arr = append(res.arr, va)
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	res.card = len(res.arr)
	return res
}

func combineAnd(a, b *container) *container {
	if a.typ == cBitmap && b.typ == cBitmap {
		res := &container{typ: cBitmap, bmp: make([]uint64, bitmapWords)}
		for i := range res.bmp {
			res.bmp[i] = a.bmp[i] & b.bmp[i]
		}
		res.card = bitmapCardinality(res.bmp)
		return res
	}
	if a.typ == cBitmap || b.typ == cBitmap {
		bm, other := a, b
		if b.typ == cBitmap {
			bm, other = b, a
		}
		res := &container{typ: cArray}
		other.foreach(func(v uint16) {
			if bm.bmp[v>>6]&(uint64(1)<<(v&63)) != 0 {
				res.arr = append(res.arr, v)
			}
		})
		res.card = len(res.arr)
		return res
	}
	return mergeIntersect(a, b)
}

func combineAndNot(a, b *container) *container {
	if a.typ == cBitmap && b.typ == cBitmap {
		res := &container{typ: cBitmap, bmp: make([]uint64, bitmapWords)}
		for i := range res.bmp {
			res.bmp[i] = a.bmp[i] &^ b.bmp[i]
		}
		res.card = bitmapCardinality(res.bmp)
		return res
	}
	res := &container{typ: cArray}
	a.foreach(func(v uint16) {
		if !b.contains(v) {
			res.arr = append(res.arr, v)
		}
	})
	res.card = len(res.arr)
	return res
}

func combineXor(a, b *container) *container {
	if a.typ == cBitmap || b.typ == cBitmap {
		res := &container{typ: cBitmap, bmp: make([]uint64, bitmapWords)}
		a.foreach(func(v uint16) { res.bmp[v>>6] ^= uint64(1) << (v & 63) })
		b.foreach(func(v uint16) { res.bmp[v>>6] ^= uint64(1) << (v & 63) })
		res.card = bitmapCardinality(res.bmp)
		return res
	}
	ia, ib := a.iter(), b.iter()
	res := &container{typ: cArray}
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka || okb {
		switch {
		case oka && (!okb || va < vb):
			res.arr = append(res.arr, va)
			va, oka = ia.next()
		case okb && (!oka || vb < va):
			res.arr = append(res.arr, vb)
			vb, okb = ib.next()
		default:
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	res.card = len(res.arr)
	return res
}

func mergeIntersect(a, b *container) *container {
	ia, ib := a.iter(), b.iter()
	res := &container{typ: cArray}
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka && okb {
		switch {
		case va < vb:
			va, oka = ia.next()
		case vb < va:
			vb, okb = ib.next()
		default:
			res.arr = append(res.arr, va)
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	res.card = len(res.arr)
	return res
}
