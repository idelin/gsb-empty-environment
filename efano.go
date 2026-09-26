// Package efano implements Elias-Fano encoding for non-decreasing
// sequences of uint64, with duplicates, O(log n) random access and
// lower_bound, a bounded-memory streaming builder, and a merge that
// works directly on the high/low bit representation.
//
// Encoding layout for n values with universe u = max+1 and low width
// l = max(0, floor(log2(u/n))):
//
//	value[i] = (high[i] << l) | low[i]
//
//	low:  n*l bits, the l low bits of every value, packed contiguously.
//	high: n + floor((u-1)/2^l) bits; for each value, (high[i]-high[i-1])
//	      zero bits followed by a one bit (unary gaps). Hence the high
//	      part of value i is select1(i) - i.
//
// Total size T(l) = n*l + n + floor((u-1)/2^l) bits. With
// l = floor(log2(u/n)) the zero count lies in [n, 2n), so
// T <= n*(log2(u/n) + 3) bits (see ChooseL for the derivation).
package efano

import (
	"math"
	"math/bits"
)

// superblockBits is the rank-directory granularity over the high vector.
const superblockBits = 512

// EliasFano is an immutable encoded sequence.
type EliasFano struct {
	n      uint64
	l      uint
	maxVal uint64   // last value; meaningless when n == 0
	high   bitvec   // unary gaps of the high parts
	low    bitvec   // packed low parts, n*l bits
	rank   []uint64 // rank[i] = ones in high before bit i*superblockBits;
	// the final entry holds the total number of ones (== n)
}

// ChooseL derives the low-bit width. With u the universe size (max+1)
// and n the element count, T(l) = n*l + n + floor((u-1)/2^l) bits is
// minimized near 2^l = (ln 2)*u/n, i.e. l* = log2(u/n) - 0.47. The
// integer choice l = floor(log2(u/n)) is within 1 of the optimum and
// keeps the unary zero count in [n, 2n), giving
//
//	T <= n*log2(u/n) + 3n  bits   (<= log2(u/n) + 3 bits/element)
//
// Degenerate cases: n == 0 or u <= n (including max < n) yield l = 0,
// so all-equal or dense sequences never divide by zero and store no
// low bits at all.
func ChooseL(universe, n uint64) uint {
	if n == 0 || universe <= n {
		return 0
	}
	// floor(universe/n) >= 1 here; floor(log2(x)) == floor(log2(floor(x)))
	// for x >= 1, so the integer ratio gives exactly floor(log2(u/n)).
	return uint(bits.Len64(universe/n) - 1)
}

// Encode builds an index over vals (must be non-decreasing) with an
// explicit low-bit width. Use ChooseL to derive l from the universe.
func Encode(vals []uint64, l uint) (*EliasFano, error) {
	b := NewBuilder(l)
	for _, v := range vals {
		if err := b.Add(v); err != nil {
			return nil, err
		}
	}
	return b.Build(), nil
}

// EncodeAuto picks l = ChooseL(max+1, n) and encodes vals.
func EncodeAuto(vals []uint64) (*EliasFano, error) {
	var u uint64
	if len(vals) > 0 {
		u = vals[len(vals)-1]
		if u < math.MaxUint64 {
			u++ // saturate instead of overflowing the universe
		}
	}
	return Encode(vals, ChooseL(u, uint64(len(vals))))
}

// Len returns the number of encoded values.
func (ef *EliasFano) Len() uint64 { return ef.n }

// LowBits returns the low-bit width l.
func (ef *EliasFano) LowBits() uint { return ef.l }

// Max returns the last (largest) value. It panics on an empty index.
func (ef *EliasFano) Max() uint64 {
	if ef.n == 0 {
		panic("efano: Max of empty sequence")
	}
	return ef.maxVal
}

func (ef *EliasFano) buildRank() {
	nblocks := (ef.high.nbits + superblockBits - 1) / superblockBits
	ef.rank = make([]uint64, nblocks+1)
	var cum uint64
	const wordsPerBlock = superblockBits / 64
	for i := uint64(0); i < nblocks; i++ {
		ef.rank[i] = cum
		start := i * wordsPerBlock
		end := start + wordsPerBlock
		if end > uint64(len(ef.high.words)) {
			end = uint64(len(ef.high.words))
		}
		for w := start; w < end; w++ {
			cum += uint64(bits.OnesCount64(ef.high.words[w]))
		}
	}
	ef.rank[nblocks] = cum
}

// rank1 returns the number of one bits in high[0:p].
func (ef *EliasFano) rank1(p uint64) uint64 {
	if p > ef.high.nbits {
		p = ef.high.nbits
	}
	blk := p / superblockBits
	r := ef.rank[blk]
	w := blk * (superblockBits / 64)
	for w < p/64 {
		r += uint64(bits.OnesCount64(ef.high.words[w]))
		w++
	}
	if rem := uint(p % 64); rem != 0 {
		r += uint64(bits.OnesCount64(ef.high.words[p/64] & (uint64(1)<<rem - 1)))
	}
	return r
}

// select1 returns the bit position of the i-th one bit (0-based).
func (ef *EliasFano) select1(i uint64) uint64 {
	lo, hi := uint64(0), ef.high.nbits
	for lo < hi {
		mid := (lo + hi) / 2
		if ef.rank1(mid+1) > i {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// select0 returns the bit position of the j-th zero bit (0-based).
func (ef *EliasFano) select0(j uint64) uint64 {
	lo, hi := uint64(0), ef.high.nbits
	for lo < hi {
		mid := (lo + hi) / 2
		if mid+1-ef.rank1(mid+1) > j {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// highStart returns the number of values whose high part is < h,
// i.e. the index of the first value with high part >= h.
func (ef *EliasFano) highStart(h uint64) uint64 {
	if h == 0 {
		return 0
	}
	zeros := ef.high.nbits - ef.n
	if h > zeros {
		return ef.n
	}
	return ef.rank1(ef.select0(h - 1))
}

// At returns the i-th value (0-based). It panics if i >= Len().
func (ef *EliasFano) At(i uint64) uint64 {
	if i >= ef.n {
		panic("efano: index out of range")
	}
	hi := ef.select1(i) - i
	lo := ef.low.getBits(i*uint64(ef.l), ef.l)
	return hi<<ef.l | lo
}

// LowerBound returns the exact rank of x: the index of the first value
// >= x, i.e. the count of values < x. Queries above the last value
// return Len() (the past-the-end position). Empty indexes return 0.
func (ef *EliasFano) LowerBound(x uint64) uint64 {
	if ef.n == 0 {
		return 0
	}
	hi := x >> ef.l
	if hi > ef.maxVal>>ef.l {
		return ef.n // x exceeds every possible value with this high part
	}
	lo := x & mask64(ef.l)
	beg := ef.highStart(hi)
	end := ef.highStart(hi + 1)
	// Binary search the low bits within the bucket [beg, end).
	for beg < end {
		mid := (beg + end) / 2
		if ef.low.getBits(mid*uint64(ef.l), ef.l) >= lo {
			end = mid
		} else {
			beg = mid + 1
		}
	}
	return beg
}

// EqualRange returns the exact half-open rank range [start, end) of
// values equal to x; end-start is the multiplicity of x.
func (ef *EliasFano) EqualRange(x uint64) (start, end uint64) {
	start = ef.LowerBound(x)
	if x == math.MaxUint64 {
		return start, ef.n // nothing can exceed x
	}
	return start, ef.LowerBound(x + 1)
}

// Count returns the multiplicity of x.
func (ef *EliasFano) Count(x uint64) uint64 {
	start, end := ef.EqualRange(x)
	return end - start
}

// Decode materializes the whole sequence (mainly for tests and tools).
func (ef *EliasFano) Decode() []uint64 {
	out := make([]uint64, ef.n)
	for i := range out {
		out[i] = ef.At(uint64(i))
	}
	return out
}

// Stats is the byte ledger of an index.
type Stats struct {
	N               uint64
	L               uint
	MaxVal          uint64
	HighBits        uint64 // unary high-part bits (ones + zeros)
	LowBits         uint64 // packed low-part bits
	RankBytes       uint64 // select/rank directory over the high vector
	IndexBytes      uint64 // bytes actually allocated: high + low + rank
	SerializedBytes uint64 // exact on-disk size of MarshalBinary output
	BitsPerValue    float64
}

// Stats computes the actual occupied-byte ledger.
func (ef *EliasFano) Stats() Stats {
	st := Stats{
		N:               ef.n,
		L:               ef.l,
		MaxVal:          ef.maxVal,
		HighBits:        ef.high.nbits,
		LowBits:         ef.low.nbits,
		RankBytes:       uint64(len(ef.rank)) * 8,
		SerializedBytes: ef.SerializedSize(),
	}
	st.IndexBytes = uint64(len(ef.high.words)+len(ef.low.words))*8 + st.RankBytes
	if ef.n > 0 {
		st.BitsPerValue = float64(ef.high.nbits+ef.low.nbits) / float64(ef.n)
	}
	return st
}

// Verify re-decodes the whole index and checks monotonicity and the
// declared maximum, independently of any query path.
func (ef *EliasFano) Verify() error {
	var prev uint64
	for i := uint64(0); i < ef.n; i++ {
		v := ef.At(i)
		if i > 0 && v < prev {
			return &CorruptError{What: "values not non-decreasing"}
		}
		prev = v
	}
	if ef.n > 0 && prev != ef.maxVal {
		return &CorruptError{What: "last value does not match header max"}
	}
	return nil
}
