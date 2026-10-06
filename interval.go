// Package batch implements a batch execution tracker: a total amount of
// work [0, total) is split into sub-intervals leased to workers. Workers
// confirm completed (sub-)intervals — typically a prefix of their lease —
// and may then fail. Re-dispatch only hands out intervals that are still
// unconfirmed. Confirmations are recorded in an interval-union set, so
// overlapping or duplicate receipts are idempotent, out-of-order receipts
// merge correctly, and completion is exactly "the union covers [0, total)".
package batch

import "sort"

// Interval is a half-open range [Start, End) of work items.
type Interval struct {
	Start int64
	End   int64
}

// Len returns the number of items in the interval.
func (iv Interval) Len() int64 { return iv.End - iv.Start }

// intervalSet is a union of disjoint, non-adjacent, sorted intervals.
type intervalSet struct {
	ivs []Interval // sorted by Start, disjoint, non-touching
}

// add merges iv into the set. Adding an interval that overlaps or touches
// existing ones is idempotent: the union is unchanged in coverage.
func (s *intervalSet) add(iv Interval) {
	if iv.Start >= iv.End {
		return
	}
	ivs := s.ivs
	// Find insertion window: all intervals with Start <= iv.End that may
	// overlap or touch [iv.Start, iv.End).
	i := sort.Search(len(ivs), func(i int) bool { return ivs[i].Start > iv.End })
	lo := i
	for lo > 0 && ivs[lo-1].End >= iv.Start {
		lo--
	}
	if lo < i {
		if ivs[lo].Start < iv.Start {
			iv.Start = ivs[lo].Start
		}
		if ivs[i-1].End > iv.End {
			iv.End = ivs[i-1].End
		}
	}
	s.ivs = append(ivs[:lo], append([]Interval{iv}, ivs[i:]...)...)
}

// contains reports whether [start, end) is fully covered by the set.
func (s *intervalSet) contains(start, end int64) bool {
	if start >= end {
		return true
	}
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i].End > start })
	return i < len(s.ivs) && s.ivs[i].Start <= start && s.ivs[i].End >= end
}

// covers reports whether the set fully covers [0, total).
func (s *intervalSet) covers(total int64) bool {
	return total <= 0 || s.contains(0, total)
}

// gaps returns the sub-intervals of [0, total) not covered by the set,
// in ascending order.
func (s *intervalSet) gaps(total int64) []Interval {
	var out []Interval
	cursor := int64(0)
	for _, iv := range s.ivs {
		if iv.End <= cursor {
			continue
		}
		if iv.Start > cursor {
			out = append(out, Interval{Start: cursor, End: iv.Start})
		}
		if iv.End > cursor {
			cursor = iv.End
		}
		if cursor >= total {
			return out
		}
	}
	if cursor < total {
		out = append(out, Interval{Start: cursor, End: total})
	}
	return out
}

// subtract returns the parts of [start, end) not covered by the set.
func (s *intervalSet) subtract(start, end int64) []Interval {
	var out []Interval
	cursor := start
	for _, iv := range s.ivs {
		if iv.End <= cursor {
			continue
		}
		if iv.Start >= end {
			break
		}
		if iv.Start > cursor {
			out = append(out, Interval{Start: cursor, End: iv.Start})
		}
		if iv.End > cursor {
			cursor = iv.End
		}
		if cursor >= end {
			return out
		}
	}
	if cursor < end {
		out = append(out, Interval{Start: cursor, End: end})
	}
	return out
}

// sum returns the total number of covered items.
func (s *intervalSet) sum() int64 {
	var n int64
	for _, iv := range s.ivs {
		n += iv.Len()
	}
	return n
}
