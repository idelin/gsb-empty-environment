// Package batchrun implements a batch execution tracker that splits a total
// workload into sub-ranges, leases them to workers, and records confirmed
// progress as a union of intervals. Workers may confirm a completed prefix
// of their lease and then fail; redispatch only covers the unconfirmed
// remainder. Overlapping confirmations are idempotent, out-of-bounds
// confirmations are rejected, and out-of-order receipts are merged by the
// interval-union model.
package batchrun

// Range is a half-open interval [Lo, Hi) of work items.
type Range struct {
	Lo, Hi int64
}

// Len returns the number of items in the range.
func (r Range) Len() int64 { return r.Hi - r.Lo }

// intervalSet is a sorted list of disjoint, non-adjacent half-open
// intervals. It models the union of confirmed work.
type intervalSet struct {
	ivs []Range
}

// add merges r into the set, coalescing overlapping and adjacent
// intervals. Adding an already-covered (overlapping) range is idempotent.
func (s *intervalSet) add(r Range) {
	if r.Lo >= r.Hi {
		return
	}
	lo, hi := r.Lo, r.Hi
	out := s.ivs[:0]
	inserted := false
	for _, iv := range s.ivs {
		switch {
		case iv.Hi < lo:
			out = append(out, iv)
		case hi < iv.Lo:
			if !inserted {
				out = append(out, Range{lo, hi})
				inserted = true
			}
			out = append(out, iv)
		default:
			// Overlapping or adjacent: absorb into the merged range.
			if iv.Lo < lo {
				lo = iv.Lo
			}
			if iv.Hi > hi {
				hi = iv.Hi
			}
		}
	}
	if !inserted {
		out = append(out, Range{lo, hi})
	}
	s.ivs = out
}

// covers reports whether r is fully contained in the union.
func (s *intervalSet) covers(r Range) bool {
	if r.Lo >= r.Hi {
		return true
	}
	for _, iv := range s.ivs {
		if iv.Lo > r.Lo {
			return false
		}
		if iv.Lo <= r.Lo && r.Hi <= iv.Hi {
			return true
		}
	}
	return false
}

// covered returns the total number of items in the union.
func (s *intervalSet) covered() int64 {
	var n int64
	for _, iv := range s.ivs {
		n += iv.Len()
	}
	return n
}

// gaps returns the sub-ranges of total not covered by the union, in order.
func (s *intervalSet) gaps(total Range) []Range {
	var out []Range
	cur := total.Lo
	for _, iv := range s.ivs {
		if iv.Hi <= cur {
			continue
		}
		if iv.Lo > cur {
			out = append(out, Range{cur, iv.Lo})
		}
		if iv.Hi > cur {
			cur = iv.Hi
		}
		if cur >= total.Hi {
			return out
		}
	}
	if cur < total.Hi {
		out = append(out, Range{cur, total.Hi})
	}
	return out
}
