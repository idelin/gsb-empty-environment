package bwt

import "sort"

// CheckpointStride is the number of BWT symbols summarized by one rank
// checkpoint. Rank queries scan at most CheckpointStride-1 bytes.
const CheckpointStride = 64

// Index is a compressed rank index over a BWT block. It supports O(1)-ish
// LF mapping (rank via periodic checkpoints), backward-search fragment
// location, and inversion by walking the LF mapping from the primary index.
//
// Memory: n bytes for the block plus (n/CheckpointStride+1)*256*4 bytes of
// checkpoints (~17 n bytes total with the default stride).
type Index struct {
	bwt         []byte
	primary     int
	total       [256]uint32 // histogram of the block
	start       [256]uint32 // C array: number of symbols smaller than b
	checkpoints []uint32    // (n/CheckpointStride+1) rows of 256 counts
}

// NewIndex builds the rank index for a transformed block. It returns
// ErrPrimaryIndex if primary is out of range.
func NewIndex(bwt []byte, primary int) (*Index, error) {
	n := len(bwt)
	if n == 0 {
		if primary != 0 {
			return nil, ErrPrimaryIndex
		}
	} else if primary < 0 || primary >= n {
		return nil, ErrPrimaryIndex
	}
	x := &Index{bwt: bwt, primary: primary}
	rows := n/CheckpointStride + 1
	x.checkpoints = make([]uint32, rows*256)
	var run [256]uint32
	for i, b := range bwt {
		if i%CheckpointStride == 0 {
			copy(x.checkpoints[i/CheckpointStride*256:], run[:])
		}
		run[b]++
		x.total[b]++
	}
	// When n is an exact multiple of the stride, rank queries with i == n
	// need one extra row holding the final totals.
	if n%CheckpointStride == 0 {
		copy(x.checkpoints[(n/CheckpointStride)*256:], run[:])
	}
	var sum uint32
	for i := 0; i < 256; i++ {
		x.start[i] = sum
		sum += x.total[i]
	}
	return x, nil
}

// Len returns the block length.
func (x *Index) Len() int { return len(x.bwt) }

// rank returns the number of occurrences of b in bwt[:i].
func (x *Index) rank(b byte, i int) uint32 {
	blk := i / CheckpointStride
	r := x.checkpoints[blk*256+int(b)]
	for j := blk * CheckpointStride; j < i; j++ {
		if x.bwt[j] == b {
			r++
		}
	}
	return r
}

// LF maps row i of the sorted rotation matrix to the row whose rotation
// starts one position earlier (cyclically) in the original block.
func (x *Index) LF(i int) int {
	b := x.bwt[i]
	return int(x.start[b]) + int(x.rank(b, i))
}

// Inverse reconstructs the original block by walking the LF mapping from
// the primary index. The output length is exactly the block length.
func (x *Index) Inverse() []byte {
	n := len(x.bwt)
	out := make([]byte, n)
	idx := x.primary
	for j := n - 1; j >= 0; j-- {
		out[j] = x.bwt[idx]
		idx = x.LF(idx)
	}
	return out
}

// Locate returns the sorted start positions of every occurrence of pat in
// the original block, using backward search over the rank index. An empty
// pattern matches nowhere and returns nil.
func (x *Index) Locate(pat []byte) []int {
	n := len(x.bwt)
	if len(pat) == 0 || n == 0 {
		return nil
	}
	lo, hi := 0, n
	for i := len(pat) - 1; i >= 0; i-- {
		b := pat[i]
		lo = int(x.start[b]) + int(x.rank(b, lo))
		hi = int(x.start[b]) + int(x.rank(b, hi))
		if lo >= hi {
			return nil
		}
	}
	// Map matrix rows back to original positions: row primary is position 0
	// and each LF step moves one position backward (cyclically).
	rowPos := make([]int, n)
	row, pos := x.primary, 0
	for k := 0; k < n; k++ {
		rowPos[row] = pos
		pos--
		if pos < 0 {
			pos += n
		}
		row = x.LF(row)
	}
	out := make([]int, 0, hi-lo)
	for r := lo; r < hi; r++ {
		out = append(out, rowPos[r])
	}
	sort.Ints(out)
	return out
}
