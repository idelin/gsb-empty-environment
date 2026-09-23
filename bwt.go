// Package bwt implements a block-based Burrows-Wheeler transform and its
// inverse for arbitrary binary data (the full byte alphabet, no sentinel).
//
// Encoding sorts all n cyclic rotations of the block with a prefix-doubling
// algorithm (counting sort on equivalence classes), never materializing any
// rotation string:
//
//	Time:   O(n log n)
//	Memory: ~5 n ints (40 n bytes on 64-bit) plus the n-byte output
//
// Rotations that compare equal (periodic blocks, e.g. all-same-byte input)
// are ordered stably by their original start position, so the transform is
// deterministic and the primary index is well defined.
//
// Decoding runs the LF mapping in O(n) time and O(n) memory.
package bwt

import (
	"errors"
	"sort"
)

// ErrPrimaryIndex is returned when the primary index is out of range.
var ErrPrimaryIndex = errors.New("bwt: primary index out of range")

// Encode computes the Burrows-Wheeler transform of src and returns the
// transformed block together with the primary index (the row of the sorted
// rotation matrix that corresponds to the original block).
//
// An empty block is defined to encode to an empty block with primary index 0.
func Encode(src []byte) (out []byte, primary int) {
	n := len(src)
	if n == 0 {
		return nil, 0
	}
	p := sortCyclicShifts(src)
	out = make([]byte, n)
	for i, s := range p {
		if s == 0 {
			primary = i
		}
		out[i] = src[(s+n-1)%n]
	}
	return out, primary
}

// Decode inverts the transform. primary must satisfy 0 <= primary < len(bwt);
// for an empty block primary must be 0. The output length is exactly len(bwt).
func Decode(bwt []byte, primary int) ([]byte, error) {
	n := len(bwt)
	if n == 0 {
		if primary != 0 {
			return nil, ErrPrimaryIndex
		}
		return []byte{}, nil
	}
	if primary < 0 || primary >= n {
		return nil, ErrPrimaryIndex
	}
	var cnt [256]int
	for _, b := range bwt {
		cnt[b]++
	}
	// starts[b] = number of symbols smaller than b (the C array).
	var starts [256]int
	sum := 0
	for i := range starts {
		starts[i] = sum
		sum += cnt[i]
	}
	// t[i] = LF(i): the row whose rotation starts one position earlier.
	t := make([]int, n)
	var occ [256]int
	for i, b := range bwt {
		t[i] = starts[b] + occ[b]
		occ[b]++
	}
	out := make([]byte, n)
	idx := primary
	for j := n - 1; j >= 0; j-- {
		out[j] = bwt[idx]
		idx = t[idx]
	}
	return out, nil
}

// sortCyclicShifts returns the start positions of the cyclic rotations of s
// in sorted order. Equal rotations are ordered by start position (stable).
// It is the classic prefix-doubling suffix sort on cyclic shifts: O(n log n)
// time, O(n) memory, and no rotation string is ever constructed.
func sortCyclicShifts(s []byte) []int {
	n := len(s)
	p := make([]int, n) // permutation: sorted rotation starts
	c := make([]int, n) // equivalence class of each position

	// Sort by single character with a stable counting sort.
	var cnt [256]int
	for _, b := range s {
		cnt[b]++
	}
	sum := 0
	for i := range cnt {
		cnt[i], sum = sum, sum+cnt[i]
	}
	for i, b := range s {
		p[cnt[b]] = i
		cnt[b]++
	}
	classes := 1
	c[p[0]] = 0
	for i := 1; i < n; i++ {
		if s[p[i]] != s[p[i-1]] {
			classes++
		}
		c[p[i]] = classes - 1
	}

	pn := make([]int, n)
	cn := make([]int, n)
	buckets := make([]int, n)
	// Double the compared prefix length each round: after round h the classes
	// distinguish prefixes of length 2^(h+1) of the (cyclically read) string.
	for h := 0; (1<<h) < n && classes < n; h++ {
		shift := 1 << h
		for i := 0; i < n; i++ {
			pn[i] = p[i] - shift
			if pn[i] < 0 {
				pn[i] += n
			}
		}
		// Stable counting sort of pn by class.
		for i := 0; i < classes; i++ {
			buckets[i] = 0
		}
		for i := 0; i < n; i++ {
			buckets[c[pn[i]]]++
		}
		sum := 0
		for i := 0; i < classes; i++ {
			buckets[i], sum = sum, sum+buckets[i]
		}
		for i := 0; i < n; i++ {
			cl := c[pn[i]]
			p[buckets[cl]] = pn[i]
			buckets[cl]++
		}
		// Recompute classes for pairs (c[i], c[i+shift]).
		cn[p[0]] = 0
		newClasses := 1
		for i := 1; i < n; i++ {
			cur, prev := p[i], p[i-1]
			if c[cur] != c[prev] || c[(cur+shift)%n] != c[(prev+shift)%n] {
				newClasses++
			}
			cn[cur] = newClasses - 1
		}
		classes = newClasses
		c, cn = cn, c
	}

	// Positions sharing a class represent identical rotations (the block is
	// periodic). Break ties stably by original start position.
	if classes < n {
		sort.SliceStable(p, func(i, j int) bool {
			a, b := p[i], p[j]
			if c[a] != c[b] {
				return c[a] < c[b]
			}
			return a < b
		})
	}
	return p
}
