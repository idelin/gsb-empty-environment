// Package bwt implements block-level Burrows-Wheeler transform and its
// inverse over the full byte alphabet without a dedicated sentinel
// symbol. Rotations that compare equal (periodic inputs) are ordered
// stably by their original starting position.
//
// Transform sorts cyclic shifts with a doubling algorithm and counting
// sort: O(n log n) time, O(n) extra memory (a handful of int slices,
// roughly 40n bytes on 64-bit platforms). No rotation strings are ever
// materialized, so blocks of tens of megabytes are practical.
//
// Inverse runs in O(n) time and O(n) memory via the LF mapping.
package bwt

import "errors"

// ErrPrimaryIndex is returned when a primary index does not address a
// row of the transform matrix.
var ErrPrimaryIndex = errors.New("bwt: primary index out of range")

// Transform computes the Burrows-Wheeler transform of block and returns
// the last column of the sorted rotation matrix together with the
// primary index (the row holding the original block). Equal rotations
// are ordered by starting position, so the transform is well defined
// for periodic inputs such as runs of a single byte.
func Transform(block []byte) (last []byte, primary int) {
	n := len(block)
	if n == 0 {
		return nil, 0
	}
	p := sortCyclicShifts(block)
	last = make([]byte, n)
	for i, start := range p {
		if start == 0 {
			primary = i
		}
		last[i] = block[(start+n-1)%n]
	}
	return last, primary
}

// Inverse reconstructs the original block from the last column produced
// by Transform and the primary index. The result has exactly len(last)
// bytes. It returns ErrPrimaryIndex if primary does not address a row.
func Inverse(last []byte, primary int) ([]byte, error) {
	n := len(last)
	if n == 0 {
		if primary != 0 {
			return nil, ErrPrimaryIndex
		}
		return []byte{}, nil
	}
	if primary < 0 || primary >= n {
		return nil, ErrPrimaryIndex
	}
	var count [256]int
	for _, b := range last {
		count[b]++
	}
	// c[b] = number of bytes in last that are smaller than b.
	var c [256]int
	total := 0
	for b := 0; b < 256; b++ {
		c[b] = total
		total += count[b]
	}
	// next[i] = LF(i): row of the rotation shifted one step left.
	next := make([]int, n)
	var occ [256]int
	for i, b := range last {
		next[i] = c[b] + occ[b]
		occ[b]++
	}
	out := make([]byte, n)
	row := primary
	for i := n - 1; i >= 0; i-- {
		out[i] = last[row]
		row = next[row]
	}
	return out, nil
}

// sortCyclicShifts returns the starting positions of the cyclic shifts
// of s in sorted order. Shifts that are equal as strings are ordered
// by starting position (stable tie-break). The doubling rounds sort by
// 2^k-symbol prefixes; a final counting pass then reorders each
// equivalence class of equal shifts by ascending original index.
func sortCyclicShifts(s []byte) []int {
	n := len(s)
	p := make([]int, n)
	class := make([]int, n)
	cnt := make([]int, n)
	if n < 256 {
		cnt = make([]int, 256)
	}

	// Sort by first character, stably.
	for _, b := range s {
		cnt[b]++
	}
	pos := make([]int, 256)
	sum := 0
	for b := 0; b < 256; b++ {
		pos[b] = sum
		sum += cnt[b]
	}
	for i, b := range s {
		p[pos[b]] = i
		pos[b]++
	}
	classes := 1
	class[p[0]] = 0
	for i := 1; i < n; i++ {
		if s[p[i]] != s[p[i-1]] {
			classes++
		}
		class[p[i]] = classes - 1
	}

	pn := make([]int, n)
	cn := make([]int, n)
	for h := 1; h < n; h <<= 1 {
		// pn holds shifts starting h earlier, already ordered by
		// their second half.
		for i := 0; i < n; i++ {
			pn[i] = p[i] - h
			if pn[i] < 0 {
				pn[i] += n
			}
		}
		// Stable counting sort by first half of the pair.
		for i := 0; i < classes; i++ {
			cnt[i] = 0
		}
		for i := 0; i < n; i++ {
			cnt[class[pn[i]]]++
		}
		for i := 1; i < classes; i++ {
			cnt[i] += cnt[i-1]
		}
		for i := n - 1; i >= 0; i-- {
			cl := class[pn[i]]
			cnt[cl]--
			p[cnt[cl]] = pn[i]
		}
		cn[p[0]] = 0
		newClasses := 1
		for i := 1; i < n; i++ {
			cur0, cur1 := class[p[i]], class[(p[i]+h)%n]
			prv0, prv1 := class[p[i-1]], class[(p[i-1]+h)%n]
			if cur0 != prv0 || cur1 != prv1 {
				newClasses++
			}
			cn[p[i]] = newClasses - 1
		}
		class, cn = cn, class
		classes = newClasses
	}

	// Equal shifts (periodic input) share a class but sit in an
	// arbitrary relative order after the doubling rounds. Reorder
	// every class by ascending starting position so the result is
	// deterministic and stable.
	start := make([]int, classes+1)
	for i := 0; i < n; i++ {
		start[class[i]+1]++
	}
	for i := 1; i <= classes; i++ {
		start[i] += start[i-1]
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		cl := class[i]
		out[start[cl]] = i
		start[cl]++
	}
	return out
}
