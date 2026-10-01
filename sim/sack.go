package sim

import "math/big"

// Range is a half-open byte interval [Start, End) in sequence space.
type Range struct {
	Start, End Seq
}

// Len returns End - Start.
func (r Range) Len() *big.Int { return r.End.Sub(r.Start) }

// SackSet is a set of acknowledged byte ranges, kept sorted, disjoint and
// coalesced. Adding an overlapping or adjacent range merges it, so every
// byte is counted exactly once.
type SackSet struct{ ranges []Range }

// Add merges r into the set. Overlapping or adjacent ranges are coalesced.
func (s *SackSet) Add(r Range) {
	if r.Start.Cmp(r.End) >= 0 {
		return
	}
	out := make([]Range, 0, len(s.ranges)+1)
	inserted := false
	for _, cur := range s.ranges {
		switch {
		case cur.End.Cmp(r.Start) < 0: // cur strictly before r
			out = append(out, cur)
		case r.End.Cmp(cur.Start) < 0: // cur strictly after r
			if !inserted {
				out = append(out, r)
				inserted = true
			}
			out = append(out, cur)
		default: // overlap or adjacency: merge
			if cur.Start.Cmp(r.Start) < 0 {
				r.Start = cur.Start
			}
			if cur.End.Cmp(r.End) > 0 {
				r.End = cur.End
			}
		}
	}
	if !inserted {
		out = append(out, r)
	}
	s.ranges = out
}

// Total returns the number of bytes covered by the union of all ranges.
func (s *SackSet) Total() *big.Int {
	sum := new(big.Int)
	for _, r := range s.ranges {
		sum.Add(sum, r.Len())
	}
	return sum
}

// Contains reports whether sequence number p is inside some range.
func (s *SackSet) Contains(p Seq) bool {
	for _, r := range s.ranges {
		if r.Start.Cmp(p) <= 0 && p.Cmp(r.End) < 0 {
			return true
		}
	}
	return false
}

// Gaps returns the complement of the set within [lo, hi): the byte ranges
// inside [lo, hi) that are NOT acknowledged.
func (s *SackSet) Gaps(lo, hi Seq) []Range {
	var gaps []Range
	cur := lo
	for _, r := range s.ranges {
		if r.End.Cmp(cur) <= 0 {
			continue
		}
		if r.Start.Cmp(hi) >= 0 {
			break
		}
		if r.Start.Cmp(cur) > 0 {
			end := r.Start
			if end.Cmp(hi) > 0 {
				end = hi
			}
			gaps = append(gaps, Range{Start: cur, End: end})
		}
		if r.End.Cmp(cur) > 0 {
			cur = r.End
		}
		if cur.Cmp(hi) >= 0 {
			break
		}
	}
	if cur.Cmp(hi) < 0 {
		gaps = append(gaps, Range{Start: cur, End: hi})
	}
	return gaps
}

// Ranges returns a copy of the coalesced ranges, in order.
func (s *SackSet) Ranges() []Range {
	out := make([]Range, len(s.ranges))
	copy(out, s.ranges)
	return out
}
