// Package cwi implements a compressed, versioned index over a sequence of
// non-negative uint64 weights. It supports prefix-sum queries, select by
// cumulative weight, and atomic batch range increments with overflow and
// negative-weight validation.
//
// Encoding layout (per block of BlockSize weights, last block may be short):
//
//	block := uvarint(blockTotal) uvarint(d0) uvarint(d1) ... uvarint(dN-1)
//
// where d_i is the delta of the local (in-block) prefix sums, i.e. d_i = w_i
// because weights are non-negative and prefix sums are monotone. Small
// weights therefore occupy a single byte. The block-total layer allows
// prefix/select queries to skip whole blocks without decoding them; only
// the blocks that overlap a query or update are ever decoded/re-encoded.
package cwi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// BlockSize is the number of weights stored per compressed block.
const BlockSize = 64

var (
	ErrOverflow = errors.New("cwi: uint64 overflow in accumulated weight")
	ErrNegative = errors.New("cwi: update would make a weight negative")
	ErrRange    = errors.New("cwi: invalid range")
	ErrVersion  = errors.New("cwi: unknown version")
)

// block is an immutable compressed run of weights.
type block struct {
	count int    // number of weights (== BlockSize except for the last block)
	total uint64 // sum of all weights in the block (redundant with enc header)
	enc   []byte // uvarint(total) followed by uvarint deltas of local prefix sums
}

// encodeBlock compresses ws into the on-disk-like byte layout. It reports
// ok=false if the block total itself overflows uint64.
func encodeBlock(ws []uint64) (b *block, ok bool) {
	b = &block{count: len(ws)}
	var total uint64
	for _, w := range ws {
		var carry uint64
		total, carry = bits.Add64(total, w, 0)
		if carry != 0 {
			return nil, false
		}
	}
	b.total = total
	var tmp [binary.MaxVarintLen64]byte
	enc := make([]byte, 0, len(ws)+binary.MaxVarintLen64)
	n := binary.PutUvarint(tmp[:], total)
	enc = append(enc, tmp[:n]...)
	prev := uint64(0)
	for _, w := range ws {
		cur := prev + w
		d := cur - prev // delta of local prefix sums == w
		n := binary.PutUvarint(tmp[:], d)
		enc = append(enc, tmp[:n]...)
		prev = cur
	}
	b.enc = enc
	return b, true
}

// decode fully expands the block. This is the only place the compressed
// layer is materialized, and it is bounded by BlockSize.
func (b *block) decode() []uint64 {
	ws := make([]uint64, 0, b.count)
	rest := b.enc
	_, m := binary.Uvarint(rest) // skip block total header
	rest = rest[m:]
	for i := 0; i < b.count; i++ {
		d, k := binary.Uvarint(rest)
		rest = rest[k:]
		ws = append(ws, d)
	}
	return ws
}

// decodePrefix expands only the first k weights of the block.
func (b *block) decodePrefix(k int) []uint64 {
	ws := make([]uint64, 0, k)
	rest := b.enc
	_, m := binary.Uvarint(rest)
	rest = rest[m:]
	for i := 0; i < k; i++ {
		d, n := binary.Uvarint(rest)
		rest = rest[n:]
		ws = append(ws, d)
	}
	return ws
}

// snapshot is one committed version: a slice of immutable blocks plus the
// precomputed total weight.
type snapshot struct {
	blocks []*block
	total  uint64
}

// Index is a versioned compressed weight index. Version 0 is the initial
// state; every successful RangeAdd appends exactly one new version and never
// mutates older ones (blocks are shared structurally).
type Index struct {
	n        int
	versions []*snapshot
}

// New builds the index from ws, checking that the total weight fits uint64.
func New(ws []uint64) (*Index, error) {
	total := uint64(0)
	for _, w := range ws {
		var carry uint64
		total, carry = bits.Add64(total, w, 0)
		if carry != 0 {
			return nil, ErrOverflow
		}
	}
	var blocks []*block
	for lo := 0; lo < len(ws); lo += BlockSize {
		hi := lo + BlockSize
		if hi > len(ws) {
			hi = len(ws)
		}
		blk, ok := encodeBlock(ws[lo:hi])
		if !ok {
			return nil, ErrOverflow
		}
		blocks = append(blocks, blk)
	}
	return &Index{n: len(ws), versions: []*snapshot{{blocks: blocks, total: total}}}, nil
}

// Len returns the number of weighted positions.
func (ix *Index) Len() int { return ix.n }

// Versions returns the number of committed versions.
func (ix *Index) Versions() int { return len(ix.versions) }

func (ix *Index) snap(ver int) (*snapshot, error) {
	if ver < 0 || ver >= len(ix.versions) {
		return nil, ErrVersion
	}
	return ix.versions[ver], nil
}

// Total returns the total weight of a version.
func (ix *Index) Total(ver int) (uint64, error) {
	s, err := ix.snap(ver)
	if err != nil {
		return 0, err
	}
	return s.total, nil
}

// AccessStats reports how many compressed blocks had to be decoded for an
// operation (block-total-only skips are free and not counted).
type AccessStats struct {
	BlocksDecoded int
}

// UpdateReport describes the re-encode footprint of a committed RangeAdd.
type UpdateReport struct {
	Version         int // newly committed version
	FirstBlock      int // first re-encoded block, -1 if none
	LastBlock       int // last re-encoded block (inclusive), -1 if none
	BlocksReencoded int
	BytesWritten    int // bytes of the newly encoded blocks
}

// RangeAdd atomically adds delta (may be negative) to every weight in
// [lo, hi) of the latest version. The whole batch is validated first: if any
// weight would become negative, or any accumulation overflows uint64, nothing
// is committed and an error is returned. On success a new version is
// appended; only the blocks overlapping [lo, hi) are re-encoded.
func (ix *Index) RangeAdd(lo, hi int, delta int64) (UpdateReport, error) {
	rep := UpdateReport{Version: len(ix.versions) - 1, FirstBlock: -1, LastBlock: -1}
	if lo < 0 || hi > ix.n || lo > hi {
		return rep, ErrRange
	}
	if lo == hi || delta == 0 {
		return rep, nil
	}
	cur := ix.versions[len(ix.versions)-1]
	fb, lb := lo/BlockSize, (hi-1)/BlockSize

	newBlocks := make([]*block, len(cur.blocks))
	copy(newBlocks, cur.blocks)

	// Phase 1: decode affected blocks, apply, validate, re-encode.
	var neg uint64 // magnitude of a negative delta, MinInt64-safe
	if delta < 0 {
		neg = uint64(-(delta + 1)) + 1
	}
	for bi := fb; bi <= lb; bi++ {
		ws := cur.blocks[bi].decode()
		s := lo - bi*BlockSize
		if s < 0 {
			s = 0
		}
		e := hi - bi*BlockSize
		if e > len(ws) {
			e = len(ws)
		}
		for i := s; i < e; i++ {
			if delta > 0 {
				nw, carry := bits.Add64(ws[i], uint64(delta), 0)
				if carry != 0 {
					return rep, ErrOverflow
				}
				ws[i] = nw
			} else {
				if ws[i] < neg {
					return rep, ErrNegative
				}
				ws[i] -= neg
			}
		}
		blk, ok := encodeBlock(ws)
		if !ok {
			return rep, ErrOverflow
		}
		newBlocks[bi] = blk
	}

	// Phase 2: validate the global total, then commit atomically.
	total := uint64(0)
	for _, b := range newBlocks {
		var carry uint64
		total, carry = bits.Add64(total, b.total, 0)
		if carry != 0 {
			return rep, ErrOverflow
		}
	}
	ix.versions = append(ix.versions, &snapshot{blocks: newBlocks, total: total})

	rep.Version = len(ix.versions) - 1
	rep.FirstBlock = fb
	rep.LastBlock = lb
	rep.BlocksReencoded = lb - fb + 1
	for bi := fb; bi <= lb; bi++ {
		rep.BytesWritten += len(newBlocks[bi].enc)
	}
	return rep, nil
}

// PrefixSum returns w[0]+...+w[i-1] (i in [0, n]) for a version.
func (ix *Index) PrefixSum(ver, i int) (uint64, AccessStats, error) {
	var st AccessStats
	s, err := ix.snap(ver)
	if err != nil {
		return 0, st, err
	}
	if i < 0 || i > ix.n {
		return 0, st, ErrRange
	}
	full, rem := i/BlockSize, i%BlockSize
	sum := uint64(0)
	for bi := 0; bi < full; bi++ {
		var carry uint64
		sum, carry = bits.Add64(sum, s.blocks[bi].total, 0)
		if carry != 0 {
			return 0, st, ErrOverflow
		}
	}
	if rem > 0 {
		ws := s.blocks[full].decodePrefix(rem)
		st.BlocksDecoded++
		for _, w := range ws {
			var carry uint64
			sum, carry = bits.Add64(sum, w, 0)
			if carry != 0 {
				return 0, st, ErrOverflow
			}
		}
	}
	return sum, st, nil
}

// Select returns the smallest index i such that PrefixSum(i+1) > target,
// i.e. the element whose weight interval contains target. Zero-weight
// elements own an empty interval and are never returned. If
// target >= total (including target == total) it returns n, the
// past-the-end position.
func (ix *Index) Select(ver int, target uint64) (int, AccessStats, error) {
	var st AccessStats
	s, err := ix.snap(ver)
	if err != nil {
		return 0, st, err
	}
	if target >= s.total {
		return ix.n, st, nil
	}
	acc := uint64(0)
	for bi, b := range s.blocks {
		if acc+b.total > target { // target lives inside this block
			ws := b.decode()
			st.BlocksDecoded++
			for k, w := range ws {
				acc += w
				if acc > target {
					return bi*BlockSize + k, st, nil
				}
			}
		}
		acc += b.total
	}
	return ix.n, st, nil
}

// StoredBytes reports the actual compressed byte footprint of a version
// (sum of encoded block sizes). Slice headers and the version bookkeeping
// are metadata and reported separately by OverheadBytes.
func (ix *Index) StoredBytes(ver int) (int, error) {
	s, err := ix.snap(ver)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range s.blocks {
		n += len(b.enc)
	}
	return n, nil
}

// TotalStoredBytes sums StoredBytes over all live versions, showing the
// cost of structural sharing: only re-encoded blocks add bytes.
func (ix *Index) TotalStoredBytes() int {
	n := 0
	for _, s := range ix.versions {
		for _, b := range s.blocks {
			n += len(b.enc)
		}
	}
	return n
}

// NaiveBytes is what an uncompressed []uint64 per version would occupy.
func (ix *Index) NaiveBytes() int { return 8 * ix.n * len(ix.versions) }

// Layout returns a human-readable description of the encoding.
func Layout() string {
	return fmt.Sprintf("block(%d weights) = uvarint(blockTotal) + %d x uvarint(delta of local prefix sums)", BlockSize, BlockSize)
}
