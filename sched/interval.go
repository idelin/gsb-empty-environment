// Package sched implements qualification-aware shift scheduling checks.
//
// All times are integer minutes. Intervals are left-closed, right-open:
// [Start, End). Boundary equality does not constitute a gap.
package sched

import "sort"

// Interval is a left-closed, right-open minute interval [Start, End).
type Interval struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Empty reports whether the interval contains no minute.
func (iv Interval) Empty() bool { return iv.Start >= iv.End }

// Overlaps reports whether iv and o share at least one minute.
func (iv Interval) Overlaps(o Interval) bool {
	return iv.Start < o.End && o.Start < iv.End
}

// MergeUnion returns the minimal sorted, disjoint union of ivs.
// Touching intervals (a.End == b.Start) are merged into one.
func MergeUnion(ivs []Interval) []Interval {
	out := make([]Interval, 0, len(ivs))
	sorted := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if !iv.Empty() {
			sorted = append(sorted, iv)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	for _, iv := range sorted {
		if n := len(out); n > 0 && iv.Start <= out[n-1].End {
			if iv.End > out[n-1].End {
				out[n-1].End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Subtract returns a \ b. Both must be merged (sorted, disjoint).
// The result is sorted, disjoint and minimal.
func Subtract(a, b []Interval) []Interval {
	var out []Interval
	i := 0
	for _, seg := range a {
		cur := seg.Start
		for i < len(b) && b[i].End <= cur {
			i++
		}
		j := i
		for j < len(b) && b[j].Start < seg.End {
			if b[j].Start > cur {
				out = append(out, Interval{cur, b[j].Start})
			}
			if b[j].End > cur {
				cur = b[j].End
			}
			if cur >= seg.End {
				break
			}
			j++
		}
		if cur < seg.End {
			out = append(out, Interval{cur, seg.End})
		}
	}
	return out
}

// Clip intersects each interval with [start, end), dropping empties.
func Clip(ivs []Interval, start, end int64) []Interval {
	var out []Interval
	for _, iv := range ivs {
		s, e := iv.Start, iv.End
		if s < start {
			s = start
		}
		if e > end {
			e = end
		}
		if s < e {
			out = append(out, Interval{s, e})
		}
	}
	return out
}

// Gaps returns the minimal disjoint intervals of [start, end) not covered
// by cover (which must be merged). Cover is clipped to [start, end) first.
func Gaps(start, end int64, cover []Interval) []Interval {
	if start >= end {
		return nil
	}
	return Subtract([]Interval{{start, end}}, MergeUnion(Clip(cover, start, end)))
}
