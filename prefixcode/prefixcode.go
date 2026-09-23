// Package prefixcode builds length-limited prefix codes using the
// package-merge algorithm (optimal under a maximum code length constraint),
// and provides a self-describing binary format with explicit version,
// lengths, and checksum for encoding and bounded decoding.
//
// Only the Go standard library is used.
package prefixcode

import (
	"errors"
	"fmt"
	"sort"
)

// MaxLimit is the largest permitted maximum code length.
// Canonical codes are kept in uint32, so 32 is a hard ceiling.
const MaxLimit = 32

// Code is a canonical length-limited prefix code over symbols 0..n-1.
type Code struct {
	// Lengths[i] is the code length (in bits) of symbol i. Always >= 1.
	Lengths []uint8
	// MaxLen is the maximum code length constraint the code was built under.
	MaxLen int

	codes []uint32 // canonical code value per symbol, right-aligned
}

// ErrInfeasible is returned when the symbol count cannot be accommodated:
// with a length limit L there are only 2^L available codewords.
var ErrInfeasible = errors.New("prefixcode: infeasible, symbol count exceeds 2^maxLen")

// BuildCode constructs an optimal prefix code for the given symbol
// frequencies subject to the constraint that every code length is <= maxLen.
//
// It refuses (ErrInfeasible) when len(freqs) > 2^maxLen, because no prefix
// code can accommodate that many symbols within the limit. It never
// truncates an unconstrained Huffman code; lengths come from package-merge,
// so the Kraft inequality always holds and no collisions are possible.
func BuildCode(freqs []uint64, maxLen int) (*Code, error) {
	n := len(freqs)
	if n == 0 {
		return nil, errors.New("prefixcode: no symbols")
	}
	if maxLen < 1 || maxLen > MaxLimit {
		return nil, fmt.Errorf("prefixcode: maxLen %d out of range [1,%d]", maxLen, MaxLimit)
	}
	if uint64(n) > uint64(1)<<uint(maxLen) {
		return nil, ErrInfeasible
	}
	if n == 1 {
		// Degenerate case: a single symbol still needs one bit so the
		// stream is non-empty and self-terminating.
		c := &Code{Lengths: []uint8{1}, MaxLen: maxLen, codes: make([]uint32, 1)}
		return c, nil
	}
	lengths := packageMerge(freqs, maxLen)
	return NewFromLengths(lengths, maxLen)
}

// NewFromLengths validates per-symbol code lengths against maxLen and the
// Kraft inequality, then assigns canonical codes. Used by BuildCode and by
// the decoder, which reads lengths from the stream header.
func NewFromLengths(lengths []uint8, maxLen int) (*Code, error) {
	n := len(lengths)
	if n == 0 {
		return nil, errors.New("prefixcode: no symbols")
	}
	if maxLen < 1 || maxLen > MaxLimit {
		return nil, fmt.Errorf("prefixcode: maxLen %d out of range [1,%d]", maxLen, MaxLimit)
	}
	if uint64(n) > uint64(1)<<uint(maxLen) {
		return nil, ErrInfeasible
	}
	var kraft uint64
	for i, l := range lengths {
		if l < 1 || int(l) > maxLen {
			return nil, fmt.Errorf("prefixcode: symbol %d has invalid length %d (limit %d)", i, l, maxLen)
		}
		kraft += uint64(1) << uint(maxLen-int(l))
	}
	if kraft > uint64(1)<<uint(maxLen) {
		return nil, fmt.Errorf("prefixcode: lengths violate Kraft inequality (%d/2^%d > 1)", kraft, maxLen)
	}
	c := &Code{Lengths: append([]uint8(nil), lengths...), MaxLen: maxLen}
	c.assignCanonical()
	return c, nil
}

// packageMerge implements the package-merge algorithm of Larmore and
// Hirschberg. It returns optimal code lengths for the given weights under
// the limit L. Runs in O(n*L) time.
func packageMerge(freqs []uint64, L int) []uint8 {
	n := len(freqs)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if freqs[order[a]] != freqs[order[b]] {
			return freqs[order[a]] < freqs[order[b]]
		}
		return order[a] < order[b]
	})
	leaves := make([]*pmNode, n)
	for i, sym := range order {
		leaves[i] = &pmNode{weight: freqs[sym], leaf: sym}
	}
	var prev []*pmNode
	for level := 0; level < L; level++ {
		// Package: pair up items of the previous level's list.
		pkgs := make([]*pmNode, 0, len(prev)/2)
		for k := 0; k+1 < len(prev); k += 2 {
			pkgs = append(pkgs, &pmNode{
				weight: prev[k].weight + prev[k+1].weight,
				leaf:   -1,
				left:   prev[k],
				right:  prev[k+1],
			})
		}
		// Merge: combine leaves and packages, sorted by weight.
		merged := make([]*pmNode, 0, n+len(pkgs))
		i, j := 0, 0
		for i < len(leaves) && j < len(pkgs) {
			if leaves[i].weight <= pkgs[j].weight {
				merged = append(merged, leaves[i])
				i++
			} else {
				merged = append(merged, pkgs[j])
				j++
			}
		}
		merged = append(merged, leaves[i:]...)
		merged = append(merged, pkgs[j:]...)
		prev = merged
	}
	// The first 2n-2 items of the final list; a symbol's code length is the
	// number of times its leaf appears among them (counting inside packages).
	counts := make([]int, n)
	for _, node := range prev[:2*n-2] {
		countLeaves(node, counts)
	}
	lengths := make([]uint8, n)
	for i := range counts {
		lengths[i] = uint8(counts[i])
	}
	return lengths
}

type pmNode struct {
	weight      uint64
	leaf        int // symbol index, or -1 for packages
	left, right *pmNode
}

func countLeaves(node *pmNode, counts []int) {
	if node.leaf >= 0 {
		counts[node.leaf]++
		return
	}
	countLeaves(node.left, counts)
	countLeaves(node.right, counts)
}

// assignCanonical assigns canonical codes: symbols ordered by (length,
// symbol index) receive consecutive code values. Canonical assignment
// guarantees a prefix-free code whenever the Kraft inequality holds.
func (c *Code) assignCanonical() {
	n := len(c.Lengths)
	type symLen struct {
		sym, l int
	}
	order := make([]symLen, 0, n)
	for i, l := range c.Lengths {
		if l > 0 {
			order = append(order, symLen{i, int(l)})
		}
	}
	sort.Slice(order, func(a, b int) bool {
		if order[a].l != order[b].l {
			return order[a].l < order[b].l
		}
		return order[a].sym < order[b].sym
	})
	c.codes = make([]uint32, n)
	var code uint32
	prevLen := 0
	for _, e := range order {
		code <<= uint(e.l - prevLen)
		c.codes[e.sym] = code
		code++
		prevLen = e.l
	}
}

// Bits returns the canonical code value and length for symbol sym.
func (c *Code) Bits(sym int) (code uint32, length int, err error) {
	if sym < 0 || sym >= len(c.Lengths) {
		return 0, 0, fmt.Errorf("prefixcode: symbol %d out of range", sym)
	}
	return c.codes[sym], int(c.Lengths[sym]), nil
}

// Kraft returns the Kraft sum expressed as num / 2^MaxLen. The code is
// valid iff num <= 2^MaxLen, and complete (no wasted codewords) iff equal.
func (c *Code) Kraft() (num uint64, complete bool) {
	for _, l := range c.Lengths {
		if l > 0 {
			num += uint64(1) << uint(c.MaxLen-int(l))
		}
	}
	return num, num == uint64(1)<<uint(c.MaxLen)
}

// Cost returns the total weighted code length sum(freq[i]*len[i]) and the
// average bits per symbol.
func (c *Code) Cost(freqs []uint64) (total uint64, avg float64) {
	var weight uint64
	for i, l := range c.Lengths {
		total += freqs[i] * uint64(l)
		weight += freqs[i]
	}
	if weight > 0 {
		avg = float64(total) / float64(weight)
	}
	return total, avg
}
