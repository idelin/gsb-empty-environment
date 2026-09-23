package bwt

import "sort"

// Index is a search structure over a BWT block supporting LF-mapping
// based pattern location. Backward search runs on the checkpointed
// (compressed) rank index; row-to-position resolution uses a full
// position table rebuilt from the decoded text.
//
// A note on correctness: without a sentinel, periodic inputs (e.g.
// "ababab" or a run of one byte) produce identical rotations, and the
// canonical LF mapping then decomposes into several cycles. In that
// situation a text position cannot be recovered by walking the LF
// mapping to a sampled row, because the walk never leaves its cycle.
// The index therefore rebuilds the exact row-to-position table with
// the same stable cyclic-shift sorter used by Transform, which is
// well defined for equal rotations.
//
// Build cost: O(n log n) time (one inverse transform plus one cyclic
// shift sort). Memory: the last column (n bytes), rank checkpoints
// (~2n bytes) and the position table (8n bytes on 64-bit).
type Index struct {
	L       []byte
	Primary int

	n    int
	c    [256]int
	rank *RankIndex
	pos  []int // pos[row] = start position of the row's rotation
}

// NewIndex builds a search index over the BWT output. It validates the
// primary index and returns ErrPrimaryIndex if it is out of range.
func NewIndex(last []byte, primary int) (*Index, error) {
	n := len(last)
	if n == 0 {
		if primary != 0 {
			return nil, ErrPrimaryIndex
		}
		return &Index{L: last, Primary: primary}, nil
	}
	text, err := Inverse(last, primary)
	if err != nil {
		return nil, err
	}
	idx := &Index{L: last, Primary: primary, n: n, rank: NewRankIndex(last)}
	var count [256]int
	for _, b := range last {
		count[b]++
	}
	total := 0
	for b := 0; b < 256; b++ {
		idx.c[b] = total
		total += count[b]
	}
	idx.pos = sortCyclicShifts(text)
	return idx, nil
}

// Locate returns the sorted starting positions of every cyclic
// occurrence of pattern in the original block. Occurrences may wrap
// around the block end; positions are taken modulo the block length.
// It returns nil when the pattern is empty, the block is empty, or the
// pattern does not occur.
func (idx *Index) Locate(pattern []byte) []int {
	n := idx.n
	if n == 0 || len(pattern) == 0 {
		return nil
	}
	// Backward search over the rank index: maintain the row interval
	// of rotations that start with the processed suffix of pattern.
	lo, hi := 0, n
	for i := len(pattern) - 1; i >= 0; i-- {
		b := pattern[i]
		lo = idx.c[b] + idx.rank.Rank(b, lo)
		hi = idx.c[b] + idx.rank.Rank(b, hi)
		if lo >= hi {
			return nil
		}
	}
	out := make([]int, 0, hi-lo)
	for r := lo; r < hi; r++ {
		out = append(out, idx.pos[r])
	}
	sort.Ints(out)
	return out
}
