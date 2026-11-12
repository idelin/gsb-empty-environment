package recovmon

// Interval is a half-open byte range [Start, End).
// The zero value is not a valid interval.
type Interval struct {
	Start int64
	End   int64
}

// Valid reports whether the interval is well formed.
func (i Interval) Valid() bool {
	return i.Start >= 0 && i.End > i.Start
}

// Contains reports whether off lies inside the interval.
func (i Interval) Contains(off int64) bool {
	return off >= i.Start && off < i.End
}

// Overlaps reports whether two half-open intervals share any byte.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}

// Intersects returns the overlapping sub-interval and true, or the zero
// value and false when the intervals are disjoint.
func (i Interval) Intersects(o Interval) (Interval, bool) {
	lo := max(i.Start, o.Start)
	hi := min(i.End, o.End)
	if lo >= hi {
		return Interval{}, false
	}
	return Interval{Start: lo, End: hi}, true
}

// addMerged inserts span into a normalized, non-overlapping interval list,
// coalescing every interval it touches.
func addMerged(list []Interval, span Interval) []Interval {
	merged := make([]Interval, 0, len(list)+1)
	inserted := false
	for _, item := range list {
		switch {
		case item.End < span.Start:
			merged = append(merged, item)
		case item.Start > span.End:
			if !inserted {
				merged = append(merged, span)
				inserted = true
			}
			merged = append(merged, item)
		default:
			span = Interval{Start: min(item.Start, span.Start), End: max(item.End, span.End)}
		}
	}
	if !inserted {
		merged = append(merged, span)
	}
	return merged
}

func containsOffset(list []Interval, off int64) bool {
	for _, item := range list {
		if item.Contains(off) {
			return true
		}
	}
	return false
}

func overlapsAny(list []Interval, span Interval) []Interval {
	var hits []Interval
	for _, item := range list {
		if item.Overlaps(span) {
			hits = append(hits, item)
		}
	}
	return hits
}

// intersectAll returns the exact sub-intervals where each entry of list
// overlaps span.
func intersectAll(list []Interval, span Interval) []Interval {
	var out []Interval
	for _, item := range list {
		if hit, ok := item.Intersects(span); ok {
			out = append(out, hit)
		}
	}
	return out
}

// coversRaw reports whether every byte of span is inside the raw interval.
func (m *Monitor) coversRaw(span Interval) bool {
	return span.Start >= m.raw.Start && span.End <= m.raw.End
}

// fieldBoundary reports whether off is the start offset of a registered field.
func (m *Monitor) fieldBoundary(off int64) bool {
	for _, f := range m.fields {
		if f.declared.Start == off {
			return true
		}
	}
	return false
}
