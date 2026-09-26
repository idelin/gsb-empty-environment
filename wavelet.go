// Package wavelet implements an integer wavelet tree over a coordinate
// compressed alphabet. It supports range k-th smallest (quantile), range
// frequency counting and random access, all in O(log sigma) time, without
// ever sorting a queried subarray.
//
// Physical layout (no maps, no per-element objects):
//   - one balanced binary tree over the compressed alphabet [0, sigma)
//   - each internal node stores a bitvector packed into []uint64 plus a
//     rank index: rank[i] = number of 1-bits in whole words bits[0:i]
//   - a single sorted []int table maps compressed ids back to the original
//     values (this is what restores negatives correctly)
package wavelet

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// ErrEmpty is returned when a query interval contains no positions.
var ErrEmpty = errors.New("wavelet: empty interval")

// ErrBounds is returned for out-of-range positions or k values.
var ErrBounds = errors.New("wavelet: index out of bounds")

type node struct {
	lo, hi      int // alphabet interval [lo, hi)
	bits        []uint64
	rank        []uint32 // rank[i] = popcount of bits[0:i]
	left, right *node
}

// Tree is a wavelet tree over a slice of ints.
type Tree struct {
	root *node
	uniq []int // sorted distinct original values; compressed id -> value
	n    int
}

// Stats reports the real memory footprint of the index, in bytes.
type Stats struct {
	Levels          int // tree depth incl. leaf level; bit levels = Levels-1
	Nodes           int // total nodes including leaves
	BitvectorBits   int // sum of used bits across all nodes (== n * Levels)
	BitvectorBytes  int // 8 * len(bits) summed over nodes
	RankIndexBytes  int // 4 * len(rank) summed over nodes
	ValueTableBytes int // 8 * len(uniq), the id -> original value table
}

// Total returns the payload bytes of the index (bitvectors + rank index +
// value table). Slice headers and node structs are excluded; they add a
// small constant per node.
func (s Stats) Total() int {
	return s.BitvectorBytes + s.RankIndexBytes + s.ValueTableBytes
}

// New builds a wavelet tree over vals. The input is coordinate compressed:
// distinct values are sorted into a table, each element is replaced by its
// rank in that table (via binary search, no hash map), and the tree is
// built over the ranks. Original values are recovered through the table,
// so negatives round-trip exactly.
func New(vals []int) (*Tree, error) {
	if len(vals) == 0 {
		return nil, errors.New("wavelet: cannot build from empty slice")
	}
	uniq := make([]int, len(vals))
	copy(uniq, vals)
	sort.Ints(uniq)
	// dedupe in place
	w := 1
	for i := 1; i < len(uniq); i++ {
		if uniq[i] != uniq[w-1] {
			uniq[w] = uniq[i]
			w++
		}
	}
	uniq = uniq[:w]

	comp := make([]int, len(vals))
	for i, v := range vals {
		comp[i] = sort.SearchInts(uniq, v) // exact hit guaranteed
	}
	t := &Tree{uniq: uniq, n: len(vals)}
	t.root = build(comp, 0, len(uniq))
	return t, nil
}

func build(vals []int, lo, hi int) *node {
	n := &node{lo: lo, hi: hi}
	if lo+1 == hi || len(vals) == 0 {
		return n
	}
	mid := lo + (hi-lo)/2
	words := (len(vals) + 63) / 64
	n.bits = make([]uint64, words)
	n.rank = make([]uint32, words+1)
	left := make([]int, 0, len(vals))
	right := make([]int, 0, len(vals))
	var ones uint32
	for i, v := range vals {
		if i > 0 && i%64 == 0 {
			n.rank[i/64] = ones
		}
		if v < mid {
			left = append(left, v)
		} else {
			n.bits[i/64] |= 1 << uint(i%64)
			ones++
			right = append(right, v)
		}
	}
	n.rank[words] = ones
	n.left = build(left, lo, mid)
	n.right = build(right, mid, hi)
	return n
}

// rank1 returns the number of 1-bits among the first p bits (p in [0, len]).
func (n *node) rank1(p int) int {
	word, off := p/64, uint(p%64)
	c := int(n.rank[word])
	if off > 0 {
		c += bits.OnesCount64(n.bits[word] & ((1 << off) - 1))
	}
	return c
}

func (n *node) rank0(p int) int { return p - n.rank1(p) }

func (n *node) bit(i int) int { return int(n.bits[i/64]>>uint(i%64)) & 1 }

// Len returns the number of indexed elements.
func (t *Tree) Len() int { return t.n }

// Access returns the original value stored at position i.
func (t *Tree) Access(i int) (int, error) {
	if i < 0 || i >= t.n {
		return 0, ErrBounds
	}
	return t.uniq[t.root.access(i)], nil
}

func (n *node) access(i int) int {
	if n.lo+1 == n.hi {
		return n.lo
	}
	if n.bit(i) == 0 {
		return n.left.access(n.rank0(i))
	}
	return n.right.access(n.rank1(i))
}

// Quantile returns the k-th smallest value (1-based: k=1 is the minimum)
// among positions [l, r] inclusive. Duplicates count with multiplicity.
// An empty interval (l > r) or an out-of-range k is rejected.
func (t *Tree) Quantile(l, r, k int) (int, error) {
	if l > r {
		return 0, ErrEmpty
	}
	if l < 0 || r >= t.n {
		return 0, ErrBounds
	}
	if k < 1 || k > r-l+1 {
		return 0, ErrBounds
	}
	return t.uniq[t.root.quantile(l, r, k-1)], nil
}

// quantile returns the compressed id of the (k+1)-th smallest in [l, r].
func (n *node) quantile(l, r, k int) int {
	if n.lo+1 == n.hi {
		return n.lo
	}
	zl, zr := n.rank0(l), n.rank0(r+1)
	if zeros := zr - zl; k < zeros {
		return n.left.quantile(zl, zr-1, k)
	} else {
		return n.right.quantile(n.rank1(l), n.rank1(r+1)-1, k-zeros)
	}
}

// RangeFreq counts positions i in [l, r] whose value v satisfies a <= v <= b.
// Values a and b are given in the original (uncompressed) domain.
func (t *Tree) RangeFreq(l, r, a, b int) (int, error) {
	if l > r {
		return 0, ErrEmpty
	}
	if l < 0 || r >= t.n {
		return 0, ErrBounds
	}
	if a > b {
		return 0, fmt.Errorf("wavelet: empty value range [%d, %d]", a, b)
	}
	loID := sort.SearchInts(t.uniq, a) // first id with uniq[id] >= a
	hiID := sort.Search(len(t.uniq), func(i int) bool { return t.uniq[i] > b }) - 1
	if loID > hiID {
		return 0, nil
	}
	return t.root.countLTE(l, r, hiID) - t.root.countLTE(l, r, loID-1), nil
}

// countLTE counts values with compressed id <= v among positions [l, r].
func (n *node) countLTE(l, r, v int) int {
	if l > r || v < n.lo {
		return 0
	}
	if n.hi-1 <= v {
		return r - l + 1
	}
	mid := n.lo + (n.hi-n.lo)/2
	if v < mid {
		return n.left.countLTE(n.rank0(l), n.rank0(r+1)-1, v)
	}
	zeros := n.rank0(r+1) - n.rank0(l) // left-child values are all <= v
	return zeros + n.right.countLTE(n.rank1(l), n.rank1(r+1)-1, v)
}

// Stats walks the tree and measures the real encoded layout.
func (t *Tree) Stats() Stats {
	var s Stats
	s.ValueTableBytes = 8 * len(t.uniq)
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		if n == nil {
			return
		}
		s.Nodes++
		if depth+1 > s.Levels && (n.left != nil || n.right != nil) {
			s.Levels = depth + 2
		}
		s.BitvectorBytes += 8 * len(n.bits)
		s.RankIndexBytes += 4 * len(n.rank)
		walk(n.left, depth+1)
		walk(n.right, depth+1)
	}
	walk(t.root, 0)
	// used bits: every level of the tree stores exactly n bits
	if s.Levels > 0 {
		s.BitvectorBits = t.n * (s.Levels - 1)
	}
	return s
}
