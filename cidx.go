// Package cidx implements a compressed, persistent, hierarchical
// partial-sum index over a sequence of non-negative uint64 weights.
//
// Encoding layout (real bit-packed blocks, not a map wrapper):
//
//	The sequence is split into fixed-size blocks of BlockSize elements.
//	Each block is stored as:
//
//	  count  uint16   number of elements in the block (last block may be short)
//	  width  uint8    bit width of the (value - base) deltas
//	  base   uint64   minimum value inside the block
//	  total  uint64   cached block sum (feeds the Fenwick layer)
//	  data   []byte   ceil(count*width/8) bytes, deltas bit-packed
//	                  little-endian, element i at bit offset i*width
//
//	On top of the blocks sits a 1-based Fenwick tree over the cached
//	block totals, giving O(log blocks) prefix sums and
//	O(log blocks + BlockSize) select-by-cumulative-weight.
//
// Versions are persistent: Set and BatchAdd validate everything first
// (negative weights, uint64 overflow) and then atomically commit a new
// Index that shares untouched blocks with the old one. Old versions
// keep working, including select-by-cumulative-weight.
package cidx

import (
	"errors"
	"math/bits"
)

// BlockSize is the number of elements per compressed block.
const BlockSize = 64

var (
	// ErrNegativeWeight is returned when an update would drive a weight below zero.
	ErrNegativeWeight = errors.New("cidx: update would make a weight negative")
	// ErrOverflow is returned when an accumulated weight or sum overflows uint64.
	ErrOverflow = errors.New("cidx: accumulated weight overflows uint64")
	// ErrOutOfRange is returned for out-of-range indices or ranges.
	ErrOutOfRange = errors.New("cidx: index out of range")
)

// blockHeaderBytes is the fixed per-block overhead: count(2)+width(1)+base(8)+total(8).
const blockHeaderBytes = 2 + 1 + 8 + 8

// ---------------------------------------------------------------------------
// bit packing
// ---------------------------------------------------------------------------

// bitsAt reads width bits starting at bitPos from a little-endian bit stream.
func bitsAt(buf []byte, bitPos int, width uint8) uint64 {
	var v uint64
	var got uint8
	bytePos := bitPos / 8
	off := uint(bitPos % 8)
	for got < width && bytePos < len(buf) {
		take := 8 - off
		if rem := uint(width - got); take > rem {
			take = rem
		}
		mask := byte((1 << take) - 1)
		v |= uint64((buf[bytePos]>>off)&mask) << got
		got += uint8(take)
		bytePos++
		off = 0
	}
	return v
}

// setBits writes the low width bits of v at bitPos in a little-endian bit stream.
func setBits(buf []byte, bitPos int, width uint8, v uint64) {
	var put uint8
	bytePos := bitPos / 8
	off := uint(bitPos % 8)
	for put < width {
		take := 8 - off
		if rem := uint(width - put); take > rem {
			take = rem
		}
		mask := byte((1 << take) - 1)
		buf[bytePos] &^= mask << off
		buf[bytePos] |= byte(v>>put) & mask << off
		put += uint8(take)
		bytePos++
		off = 0
	}
}

// ---------------------------------------------------------------------------
// compressed block
// ---------------------------------------------------------------------------

type block struct {
	count uint16
	width uint8
	base  uint64
	total uint64
	data  []byte
}

// bytes reports the encoded size of the block in bytes.
func (b *block) bytes() int { return blockHeaderBytes + len(b.data) }

// encodeBlock builds a compressed block from raw values, checking sum overflow.
func encodeBlock(vals []uint64) (*block, error) {
	if len(vals) == 0 || len(vals) > BlockSize {
		return nil, ErrOutOfRange
	}
	base := vals[0]
	for _, v := range vals[1:] {
		if v < base {
			base = v
		}
	}
	var maxDelta, total uint64
	for _, v := range vals {
		var carry uint64
		total, carry = bits.Add64(total, v, 0)
		if carry != 0 {
			return nil, ErrOverflow
		}
		if d := v - base; d > maxDelta {
			maxDelta = d
		}
	}
	width := uint8(bits.Len64(maxDelta))
	data := make([]byte, (len(vals)*int(width)+7)/8)
	for i, v := range vals {
		setBits(data, i*int(width), width, v-base)
	}
	return &block{count: uint16(len(vals)), width: width, base: base, total: total, data: data}, nil
}

// at decodes element i of the block.
func (b *block) at(i int) uint64 {
	if b.width == 0 {
		return b.base
	}
	return b.base + bitsAt(b.data, i*int(b.width), b.width)
}

// decode expands the whole block (used only when the block is re-encoded).
func (b *block) decode() []uint64 {
	vals := make([]uint64, b.count)
	for i := range vals {
		vals[i] = b.at(i)
	}
	return vals
}

// prefix returns the sum of the first k elements of the block.
func (b *block) prefix(k int) uint64 {
	var s uint64
	for i := 0; i < k; i++ {
		s += b.at(i)
	}
	return s
}

// ---------------------------------------------------------------------------
// index
// ---------------------------------------------------------------------------

// Stats reports how much of the structure a query touched.
type Stats struct {
	BlocksAccessed int // compressed blocks decoded
	FenwickNodes   int // Fenwick nodes visited
}

// Index is an immutable compressed partial-sum index.
type Index struct {
	n      int
	blocks []*block
	fen    []uint64 // 1-based Fenwick tree over block totals
	total  uint64
}

// New builds a zero-weight index of length n.
func New(n int) *Index {
	if n < 0 {
		panic("cidx: negative length")
	}
	nb := (n + BlockSize - 1) / BlockSize
	ix := &Index{n: n, blocks: make([]*block, nb), fen: make([]uint64, nb+1)}
	for bi := 0; bi < nb; bi++ {
		cnt := BlockSize
		if rem := n - bi*BlockSize; rem < cnt {
			cnt = rem
		}
		blk, err := encodeBlock(make([]uint64, cnt))
		if err != nil {
			panic(err)
		}
		ix.blocks[bi] = blk
	}
	return ix
}

// Len returns the number of elements.
func (ix *Index) Len() int { return ix.n }

// Total returns the sum of all weights.
func (ix *Index) Total() uint64 { return ix.total }

// At decodes the weight at index i.
func (ix *Index) At(i int) uint64 {
	if i < 0 || i >= ix.n {
		panic(ErrOutOfRange)
	}
	return ix.blocks[i/BlockSize].at(i % BlockSize)
}

// fenPrefix returns the sum of the first b block totals, counting nodes.
func fenPrefix(fen []uint64, b int, st *Stats) uint64 {
	var s uint64
	for i := b; i > 0; i -= i & -i {
		s += fen[i]
		st.FenwickNodes++
	}
	return s
}

// Prefix returns the sum of the first i weights, i in [0, n].
func (ix *Index) Prefix(i int) uint64 {
	s, _ := ix.prefix(i)
	return s
}

// PrefixStats is Prefix with access statistics.
func (ix *Index) PrefixStats(i int) (uint64, Stats) { return ix.prefix(i) }

func (ix *Index) prefix(i int) (uint64, Stats) {
	if i < 0 || i > ix.n {
		panic(ErrOutOfRange)
	}
	var st Stats
	full, rem := i/BlockSize, i%BlockSize
	s := fenPrefix(ix.fen, full, &st)
	if rem > 0 {
		s += ix.blocks[full].prefix(rem)
		st.BlocksAccessed++
	}
	return s, st
}

// Select returns the smallest index i whose cumulative weight covers target,
// i.e. Prefix(i) <= target < Prefix(i+1). Zero-weight elements are never
// selected. If target >= Total() it returns n (past-the-end, by convention).
func (ix *Index) Select(target uint64) int {
	i, _ := ix.SelectStats(target)
	return i
}

// SelectStats is Select with access statistics.
func (ix *Index) SelectStats(target uint64) (int, Stats) {
	var st Stats
	if target >= ix.total {
		return ix.n, st
	}
	nb := len(ix.blocks)
	// Fenwick descent: largest idx with prefix-of-block-totals(idx) <= target.
	step := 1 << (bits.Len(uint(nb)) - 1)
	idx := 0
	var cum uint64
	for ; step > 0; step >>= 1 {
		next := idx + step
		if next <= nb && cum+ix.fen[next] <= target {
			cum += ix.fen[next]
			idx = next
			st.FenwickNodes++
		}
	}
	blk := ix.blocks[idx]
	st.BlocksAccessed++
	local := target - cum
	var run uint64
	for i := 0; i < int(blk.count); i++ {
		v := blk.at(i)
		// run+v never overflows: it is bounded by the checked block total.
		if run+v > local {
			return idx*BlockSize + i, st
		}
		run += v
	}
	panic("cidx: corrupt index")
}

// Bytes reports the real encoded footprint: block headers, packed data,
// and the Fenwick layer.
func (ix *Index) Bytes() int {
	b := 8 + 8 // n + total
	for _, blk := range ix.blocks {
		b += blk.bytes()
	}
	b += len(ix.fen) * 8
	return b
}

// BlockBytes returns the encoded size of block bi (for layout reporting).
func (ix *Index) BlockBytes(bi int) int { return ix.blocks[bi].bytes() }

// NumBlocks returns the number of compressed blocks.
func (ix *Index) NumBlocks() int { return len(ix.blocks) }

// ---------------------------------------------------------------------------
// updates (persistent: validate first, then atomically commit a new version)
// ---------------------------------------------------------------------------

// commit builds a new version with the given replacement blocks, repairing
// only the Fenwick nodes that cover changed blocks. Decreases are applied
// before increases so no intermediate Fenwick node can overflow.
func (ix *Index) commit(repl map[int]*block) (*Index, error) {
	blocks := make([]*block, len(ix.blocks))
	copy(blocks, ix.blocks)
	fen := make([]uint64, len(ix.fen))
	copy(fen, ix.fen)
	type delta struct {
		idx      int
		dec, inc uint64
	}
	var deltas []delta
	for bi, nb := range repl {
		old := blocks[bi].total
		blocks[bi] = nb
		d := delta{idx: bi}
		if nb.total >= old {
			d.inc = nb.total - old
		} else {
			d.dec = old - nb.total
		}
		deltas = append(deltas, d)
	}
	for _, d := range deltas { // decreases: always safe
		if d.dec == 0 {
			continue
		}
		for j := d.idx + 1; j < len(fen); j += j & -j {
			fen[j] -= d.dec
		}
	}
	for _, d := range deltas { // increases: checked
		if d.inc == 0 {
			continue
		}
		for j := d.idx + 1; j < len(fen); j += j & -j {
			s, carry := bits.Add64(fen[j], d.inc, 0)
			if carry != 0 {
				return nil, ErrOverflow
			}
			fen[j] = s
		}
	}
	var st Stats
	total := fenPrefix(fen, len(blocks), &st)
	return &Index{n: ix.n, blocks: blocks, fen: fen, total: total}, nil
}

// Set returns a new version with weight i replaced by v. The receiver is
// unchanged, so the old version keeps supporting prefix and select queries.
func (ix *Index) Set(i int, v uint64) (*Index, error) {
	if i < 0 || i >= ix.n {
		return nil, ErrOutOfRange
	}
	bi := i / BlockSize
	vals := ix.blocks[bi].decode()
	vals[i%BlockSize] = v
	nb, err := encodeBlock(vals)
	if err != nil {
		return nil, err
	}
	return ix.commit(map[int]*block{bi: nb})
}

// RangeOp adds Delta (signed) to every weight in [Lo, Hi).
type RangeOp struct {
	Lo, Hi int
	Delta  int64
}

// Report describes what a committed BatchAdd did.
type Report struct {
	BlocksTouched  int // distinct blocks re-encoded
	ReencodeLo     int // first re-encoded block, -1 if none
	ReencodeHi     int // one past the last re-encoded block
	ElementUpdates int // element updates applied (counted per op)
}

// addDelta applies a signed delta to a non-negative weight with checks.
func addDelta(v uint64, d int64) (uint64, error) {
	if d >= 0 {
		s, carry := bits.Add64(v, uint64(d), 0)
		if carry != 0 {
			return 0, ErrOverflow
		}
		return s, nil
	}
	mag := uint64(-(d + 1)) + 1 // handles math.MinInt64
	if mag > v {
		return 0, ErrNegativeWeight
	}
	return v - mag, nil
}

// BatchAdd validates every range increment (no negative weights, no uint64
// overflow, ranges in bounds) and, only if all of them pass, atomically
// commits a new version. On any error the receiver is returned unchanged
// and remains fully usable, including select-by-cumulative-weight.
func (ix *Index) BatchAdd(ops []RangeOp) (*Index, Report, error) {
	rep := Report{ReencodeLo: -1}
	for _, op := range ops {
		if op.Lo < 0 || op.Hi > ix.n || op.Lo > op.Hi {
			return ix, rep, ErrOutOfRange
		}
	}
	affected := make(map[int]bool)
	for _, op := range ops {
		if op.Lo == op.Hi {
			continue
		}
		for b := op.Lo / BlockSize; b <= (op.Hi-1)/BlockSize; b++ {
			affected[b] = true
		}
	}
	repl := make(map[int]*block, len(affected))
	for bi := range affected {
		vals := ix.blocks[bi].decode()
		base := bi * BlockSize
		for _, op := range ops {
			lo := op.Lo
			if lo < base {
				lo = base
			}
			hi := op.Hi
			if hi > base+len(vals) {
				hi = base + len(vals)
			}
			for i := lo; i < hi; i++ {
				nv, err := addDelta(vals[i-base], op.Delta)
				if err != nil {
					return ix, rep, err
				}
				vals[i-base] = nv
				rep.ElementUpdates++
			}
		}
		nb, err := encodeBlock(vals)
		if err != nil {
			return ix, rep, err
		}
		repl[bi] = nb
	}
	nix, err := ix.commit(repl)
	if err != nil {
		return ix, Report{ReencodeLo: -1}, err
	}
	rep.BlocksTouched = len(repl)
	if len(repl) > 0 {
		lo, hi := len(ix.blocks), 0
		for bi := range repl {
			if bi < lo {
				lo = bi
			}
			if bi > hi {
				hi = bi
			}
		}
		rep.ReencodeLo, rep.ReencodeHi = lo, hi+1
	}
	return nix, rep, nil
}
