package sched

import "sort"

// Minutes is a timestamp expressed in integer minutes.
type Minutes int64

// Interval is a half-open interval [Start, End).
type Interval struct {
	Start Minutes `json:"start"`
	End   Minutes `json:"end"`
}

// Empty reports whether the interval contains no point in time.
func (iv Interval) Empty() bool { return iv.Start >= iv.End }

// Intersects reports whether iv and o share at least one minute.
// Touching boundaries (iv.End == o.Start) do not intersect.
func (iv Interval) Intersects(o Interval) bool {
	return iv.Start < o.End && o.Start < iv.End
}

// Merge sorts intervals and merges overlapping or touching ones, so two
// certificates where one ends exactly when the other begins count as
// seamless coverage. The result is the minimal set of disjoint intervals.
func Merge(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return nil
	}
	sorted := make([]Interval, len(ivs))
	copy(sorted, ivs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	out := []Interval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]
		if iv.Start <= last.End {
			if iv.End > last.End {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Subtract returns the minimal disjoint intervals of iv not covered by
// cover. cover may be unsorted and overlapping.
func Subtract(iv Interval, cover []Interval) []Interval {
	if iv.Empty() {
		return nil
	}
	var out []Interval
	cur := iv.Start
	for _, c := range Merge(cover) {
		if c.End <= cur {
			continue
		}
		if c.Start >= iv.End {
			break
		}
		if c.Start > cur {
			end := c.Start
			if iv.End < end {
				end = iv.End
			}
			out = append(out, Interval{Start: cur, End: end})
		}
		if c.End > cur {
			cur = c.End
		}
		if cur >= iv.End {
			break
		}
	}
	if cur < iv.End {
		out = append(out, Interval{Start: cur, End: iv.End})
	}
	return out
}
