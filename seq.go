package netsec

import "sort"

// SeqNum is a 32-bit modular sequence number. Sequence numbers wrap
// around the full uint32 circle, so comparisons must use modular
// arithmetic rather than plain integer ordering.
type SeqNum uint32

// HalfCircle is half of the 32-bit sequence space. A log is only
// unambiguously ordered when all of its live sequence numbers fit in a
// window strictly smaller than HalfCircle.
const HalfCircle uint64 = 1 << 31

// SeqLess reports whether a precedes b in modular sequence space. It is
// only meaningful when a and b are known to lie within HalfCircle of
// each other.
func SeqLess(a, b SeqNum) bool {
	return int32(uint32(a)-uint32(b)) < 0
}

// windowSpan finds the minimal circular arc covering every sequence
// number in seqs. It returns the origin (the oldest sequence number on
// that arc) and the arc length. The arc length is the "window" that must
// stay below HalfCircle for modular ordering to be well defined.
func windowSpan(seqs []SeqNum) (origin SeqNum, span uint64) {
	n := len(seqs)
	if n == 0 {
		return 0, 0
	}
	sorted := make([]uint32, n)
	for i, s := range seqs {
		sorted[i] = uint32(s)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	// The minimal covering arc is the whole circle minus the largest
	// gap between consecutive sequence numbers (including the gap that
	// wraps across zero). The origin is the first sequence number after
	// that largest gap.
	var maxGap uint64
	origin = SeqNum(sorted[0])
	for i := 0; i < n; i++ {
		next := sorted[(i+1)%n]
		gap := uint64(next) - uint64(sorted[i])
		if i == n-1 {
			gap += 1 << 32
		}
		if gap > maxGap {
			maxGap = gap
			origin = SeqNum(next)
		}
	}
	return origin, (1 << 32) - maxGap
}

// seqKey maps seq to a linear key relative to origin. It is only valid
// when every sequence number in play lies within HalfCircle of origin.
func seqKey(seq, origin SeqNum) uint32 {
	return uint32(seq - origin)
}
