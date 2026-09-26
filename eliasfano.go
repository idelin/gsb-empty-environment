// Package eliasfano implements Elias-Fano encoding for non-decreasing
// sequences of non-negative integers, with duplicates allowed.
//
// # Encoding layout (real, verifiable — no map wrapper)
//
// Given n values v[0..n) in [0, U):
//
//	l          = max(0, floor(log2(U/n)))     low bits kept per value
//	low bits   : n*l bits, packed little-endian into 64-bit words
//	high bits  : unary bucket gaps — a bitvector of n ones and
//	             numBuckets zeros, where the i-th value with high part
//	             h = v[i]>>l sets the bit at position h+i
//	numBuckets = ceil(U / 2^l) <= 2n  (>= n when U > n)
//
// # Space derivation (parameter choice vs occupancy)
//
// Since 2^l <= U/n < 2^(l+1):
//
//	low  = n*l                       bits
//	high = n ones + numBuckets zeros, n <= numBuckets <= 2n
//	     => 2n <= high <= 3n         bits
//	total <= n*(l+3) + O(1)          bits
//	     ~= n*(log2(U/n) + 3)        bits  (classic Elias-Fano bound)
//
// The serialized form adds a fixed 8-byte magic, six uvarint header
// fields and an 8-byte CRC64 checksum (see serialize.go). The checksum
// makes the format independently verifiable: any single-bit corruption
// of the payload is detected by Parse.
//
// Memory is bounded by the compressed output size, never by the
// universe U: a universe of 2^62 with 4 values occupies well under
// 100 bytes.
package eliasfano

import (
	"errors"
	"fmt"
	"math/bits"
)

// EliasFano is an immutable encoded non-decreasing integer sequence.
type EliasFano struct {
	n          uint64 // number of values
	universe   uint64 // values are in [0, universe)
	lowBits    uint64 // l: low bits stored per value
	numBuckets uint64 // number of high-part buckets
	highBits   uint64 // n + numBuckets, length of the high bitvector
	low        []uint64
	high       []uint64
}

// lowBitsFor derives l = max(0, floor(log2(U/n))).
// U <= n (e.g. max value smaller than the count) yields l = 0.
func lowBitsFor(n, universe uint64) uint64 {
	if n == 0 || universe <= n {
		return 0
	}
	return uint64(bits.Len64(universe/n) - 1)
}

// numBucketsFor derives ceil(U / 2^l); 0 for empty universes/sequences.
func numBucketsFor(n, universe, lowBits uint64) uint64 {
	if n == 0 || universe == 0 {
		return 0
	}
	return ((universe - 1) >> lowBits) + 1
}

// New encodes a non-decreasing slice. universe must exceed every value.
func New(values []uint64, universe uint64) (*EliasFano, error) {
	b := NewBuilder(uint64(len(values)), universe)
	for _, v := range values {
		if err := b.Add(v); err != nil {
			return nil, err
		}
	}
	return b.Build()
}

// N returns the number of encoded values.
func (ef *EliasFano) N() uint64 { return ef.n }

// Universe returns the upper bound U of the value domain [0, U).
func (ef *EliasFano) Universe() uint64 { return ef.universe }

// LowBits returns l, the number of low bits stored per value.
func (ef *EliasFano) LowBits() uint64 { return ef.lowBits }

// NumBuckets returns the number of high-part buckets.
func (ef *EliasFano) NumBuckets() uint64 { return ef.numBuckets }

// Access returns the i-th value (0-based). Panics if i >= N().
func (ef *EliasFano) Access(i uint64) uint64 {
	if i >= ef.n {
		panic(fmt.Sprintf("eliasfano: Access(%d) out of range (n=%d)", i, ef.n))
	}
	high := select1(ef.high, i) - i
	low := getBits(ef.low, i*ef.lowBits, ef.lowBits)
	return (high << ef.lowBits) | low
}

// LowerBound returns the exact rank of v: the count of values strictly
// less than v, i.e. the index of the first value >= v. Queries above
// the last value return N() (the past-the-end position). Empty sets
// always return 0. Runs in O(n/64 + bucket) word operations.
func (ef *EliasFano) LowerBound(v uint64) uint64 {
	if ef.n == 0 || v >= ef.universe {
		return ef.n
	}
	h := v >> ef.lowBits
	lo := ef.bucketStart(h)
	hi := ef.bucketStart(h + 1)
	target := v & (ef.lowMask())
	// Low bits inside one high bucket are non-decreasing: binary search.
	for lo < hi {
		mid := lo + (hi-lo)/2
		if getBits(ef.low, mid*ef.lowBits, ef.lowBits) >= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// Rank is an alias for LowerBound: exact count of values < v.
func (ef *EliasFano) Rank(v uint64) uint64 { return ef.LowerBound(v) }

// Count returns the exact multiplicity of v (handles duplicates).
func (ef *EliasFano) Count(v uint64) uint64 {
	if v >= ef.universe {
		return 0
	}
	// v+1 cannot overflow: v < universe <= math.MaxUint64.
	return ef.LowerBound(v+1) - ef.LowerBound(v)
}

// bucketStart returns the index of the first value whose high part is
// >= h. The k-th zero of the high bitvector terminates bucket k, so the
// ones before it are exactly the values with high part <= k; hence the
// first index with high part >= h is the one-count before zero h-1.
// h >= numBuckets yields n.
func (ef *EliasFano) bucketStart(h uint64) uint64 {
	if h == 0 {
		return 0
	}
	if h > ef.numBuckets {
		return ef.n
	}
	return select0(ef.high, ef.highBits, h-1) - (h - 1)
}

func (ef *EliasFano) lowMask() uint64 {
	if ef.lowBits == 0 {
		return 0
	}
	return uint64(1)<<ef.lowBits - 1
}

// Iter streams values in order, walking the high/low indexes directly
// with O(1) amortized work per step and O(1) extra memory.
type Iter struct {
	ef      *EliasFano
	idx     uint64 // index of the next value
	scanPos uint64 // next bit position to scan in the high bitvector
}

// Iter returns an iterator positioned before the first value.
func (ef *EliasFano) Iter() *Iter { return &Iter{ef: ef} }

// Next returns the next value and true, or false when exhausted.
func (it *Iter) Next() (uint64, bool) {
	if it.idx >= it.ef.n {
		return 0, false
	}
	pos := nextSetBit(it.ef.high, it.scanPos)
	high := pos - it.idx
	low := getBits(it.ef.low, it.idx*it.ef.lowBits, it.ef.lowBits)
	it.scanPos = pos + 1
	it.idx++
	return (high << it.ef.lowBits) | low, true
}

// Merge combines two encoded sequences into one, reading the high/low
// bit indexes of both inputs directly (no full decode into memory) and
// streaming the result into a Builder. Extra memory is O(1) besides
// the compressed output. The result universe is max(a, b).
func Merge(a, b *EliasFano) *EliasFano {
	universe := a.universe
	if b.universe > universe {
		universe = b.universe
	}
	bld := NewBuilder(a.n+b.n, universe)
	ia, ib := a.Iter(), b.Iter()
	va, oka := ia.Next()
	vb, okb := ib.Next()
	emit := func(v uint64) {
		// Unreachable for valid inputs: merged values are non-decreasing
		// and below the merged universe by construction.
		if err := bld.Add(v); err != nil {
			panic(err)
		}
	}
	for oka && okb {
		if va <= vb {
			emit(va)
			va, oka = ia.Next()
		} else {
			emit(vb)
			vb, okb = ib.Next()
		}
	}
	for oka {
		emit(va)
		va, oka = ia.Next()
	}
	for okb {
		emit(vb)
		vb, okb = ib.Next()
	}
	return bld.MustBuild()
}

// Layout describes the real on-disk structure and byte occupancy.
type Layout struct {
	N            uint64
	Universe     uint64
	LowBits      uint64
	NumBuckets   uint64
	HighBitsLen  uint64
	LowWords     int
	HighWords    int
	PayloadBytes int
	TotalBytes   int
	BitsPerValue float64
}

// Layout returns the structural parameters and byte accounting.
func (ef *EliasFano) Layout() Layout {
	lay := Layout{
		N:            ef.n,
		Universe:     ef.universe,
		LowBits:      ef.lowBits,
		NumBuckets:   ef.numBuckets,
		HighBitsLen:  ef.highBits,
		LowWords:     len(ef.low),
		HighWords:    len(ef.high),
		PayloadBytes: 8 * (len(ef.low) + len(ef.high)),
		TotalBytes:   ef.SizeBytes(),
	}
	if ef.n > 0 {
		lay.BitsPerValue = float64(8*lay.PayloadBytes) / float64(ef.n)
	}
	return lay
}

// HighString renders the high unary bitvector ('1' = value marker,
// '0' = bucket separator), most useful for small examples.
func (ef *EliasFano) HighString() string {
	out := make([]byte, ef.highBits)
	for i := range out {
		out[i] = '0'
	}
	for wi, w := range ef.high {
		for b := uint64(0); b < 64; b++ {
			if pos := uint64(wi)*64 + b; pos < ef.highBits && w&(1<<b) != 0 {
				out[pos] = '1'
			}
		}
	}
	return string(out)
}

// errCorrupt is returned by Parse for any format violation.
var errCorrupt = errors.New("eliasfano: corrupt or inconsistent encoding")

// --- bit primitives ---

// getBits extracts width (<= 63) bits starting at absolute bit pos.
func getBits(words []uint64, pos, width uint64) uint64 {
	if width == 0 {
		return 0
	}
	wi, off := pos/64, pos%64
	v := words[wi] >> off
	if off+width > 64 {
		v |= words[wi+1] << (64 - off)
	}
	return v & (uint64(1)<<width - 1)
}

// select1 returns the bit position of the k-th 1-bit (0-based k).
func select1(words []uint64, k uint64) uint64 {
	remaining := k
	for wi, w := range words {
		c := uint64(bits.OnesCount64(w))
		if remaining < c {
			for b := uint64(0); b < 64; b++ {
				if w&(1<<b) != 0 {
					if remaining == 0 {
						return uint64(wi)*64 + b
					}
					remaining--
				}
			}
		}
		remaining -= c
	}
	panic("eliasfano: select1 out of range")
}

// select0 returns the bit position of the k-th 0-bit (0-based k) within
// [0, totalBits). Bits beyond totalBits are treated as 1.
func select0(words []uint64, totalBits, k uint64) uint64 {
	remaining := k
	for wi := 0; uint64(wi)*64 < totalBits; wi++ {
		inv := ^words[wi]
		bitStart := uint64(wi) * 64
		if valid := totalBits - bitStart; valid < 64 {
			inv &= uint64(1)<<valid - 1
		}
		c := uint64(bits.OnesCount64(inv))
		if remaining < c {
			for b := uint64(0); b < 64; b++ {
				if inv&(1<<b) != 0 {
					if remaining == 0 {
						return bitStart + b
					}
					remaining--
				}
			}
		}
		remaining -= c
	}
	panic("eliasfano: select0 out of range")
}

// nextSetBit returns the position of the first 1-bit at or after pos.
func nextSetBit(words []uint64, pos uint64) uint64 {
	wi, off := pos/64, pos%64
	for wi < uint64(len(words)) {
		if w := words[wi] >> off; w != 0 {
			return wi*64 + off + uint64(bits.TrailingZeros64(w))
		}
		wi++
		off = 0
	}
	panic("eliasfano: nextSetBit out of range")
}
