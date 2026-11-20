package handover

// PageBits is the width of the page-number space. Page numbers are unsigned
// and range from 0 to 2^PageBits-1. All interval operations are structural:
// the component never allocates a per-page array indexed by page number.
const PageBits = 48

// MaxPage is the largest valid page number.
const MaxPage uint64 = (uint64(1) << PageBits) - 1

// Interval is a half-open, page-aligned range [Start, End) of page numbers.
// End is exclusive and Start < End.
type Interval struct {
	Start uint64
	End   uint64
}

// Len returns the number of pages in the interval.
func (i Interval) Len() uint64 { return i.End - i.Start }

// Contains reports whether page p lies inside the interval.
func (i Interval) Contains(p uint64) bool { return i.Start <= p && p < i.End }

// IntervalSet is a sorted, disjoint, merged list of half-open intervals
// covering pages in the 48-bit page-number space. An empty set is represented
// by a nil or zero-length slice.
type IntervalSet []Interval

// checkInterval validates a single interval against the page-number space.
func checkInterval(i Interval) error {
	if i.Start >= i.End {
		return errf("invalid interval [%d,%d): start must be less than end", i.Start, i.End)
	}
	if i.End > MaxPage+1 {
		return errf("interval [%d,%d) escapes the %d-bit page space", i.Start, i.End, PageBits)
	}
	return nil
}

// Normalize sorts and merges a collection of intervals into a canonical
// IntervalSet. Inputs may overlap or be out of order; the result is sorted,
// disjoint and adjacent intervals are coalesced.
func Normalize(in []Interval) (IntervalSet, error) {
	if len(in) == 0 {
		return nil, nil
	}
	checked := make([]Interval, len(in))
	for idx, iv := range in {
		if err := checkInterval(iv); err != nil {
			return nil, err
		}
		checked[idx] = iv
	}
	sortIntervals(checked)
	out := make(IntervalSet, 0, len(checked))
	cur := checked[0]
	for _, iv := range checked[1:] {
		if iv.Start <= cur.End {
			if iv.End > cur.End {
				cur.End = iv.End
			}
			continue
		}
		out = append(out, cur)
		cur = iv
	}
	out = append(out, cur)
	return out, nil
}

// Union returns the union of two normalized interval sets.
func Union(a, b IntervalSet) IntervalSet {
	if len(a) == 0 {
		return cloneSet(b)
	}
	if len(b) == 0 {
		return cloneSet(a)
	}
	out := make(IntervalSet, 0, len(a)+len(b))
	ia, ib := 0, 0
	for ia < len(a) || ib < len(b) {
		var iv Interval
		if ib == len(b) || (ia < len(a) && a[ia].Start <= b[ib].Start) {
			iv = a[ia]
			ia++
		} else {
			iv = b[ib]
			ib++
		}
		if len(out) > 0 && iv.Start <= out[len(out)-1].End {
			if iv.End > out[len(out)-1].End {
				out[len(out)-1].End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Intersect returns the intersection of two normalized interval sets.
func Intersect(a, b IntervalSet) IntervalSet {
	var out IntervalSet
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		lo := maxU64(a[ia].Start, b[ib].Start)
		hi := minU64(a[ia].End, b[ib].End)
		if lo < hi {
			out = append(out, Interval{lo, hi})
		}
		if a[ia].End < b[ib].End {
			ia++
		} else {
			ib++
		}
	}
	return out
}

// Subtract returns the pages in a that are not in b.
func Subtract(a, b IntervalSet) IntervalSet {
	if len(a) == 0 {
		return nil
	}
	if len(b) == 0 {
		return cloneSet(a)
	}
	var out IntervalSet
	ib := 0
	for _, iv := range a {
		cursor := iv.Start
		for ib < len(b) && b[ib].End <= iv.Start {
			ib++
		}
		j := ib
		for j < len(b) && b[j].Start < iv.End {
			if b[j].Start > cursor {
				out = append(out, Interval{cursor, b[j].Start})
			}
			if b[j].End > cursor {
				cursor = b[j].End
			}
			j++
		}
		if cursor < iv.End {
			out = append(out, Interval{cursor, iv.End})
		}
	}
	return out
}

// EqualSet reports whether two normalized interval sets describe the same
// pages.
func EqualSet(a, b IntervalSet) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

// IsEmpty reports whether the set contains no pages.
func (s IntervalSet) IsEmpty() bool { return len(s) == 0 }

// Contains reports whether page p belongs to the set.
func (s IntervalSet) Contains(p uint64) bool {
	for _, iv := range s {
		if iv.Contains(p) {
			return true
		}
		if iv.Start > p {
			return false
		}
	}
	return false
}

// PageCount returns the total number of pages represented by the set without
// materializing any per-page array.
func (s IntervalSet) PageCount() uint64 {
	var total uint64
	for _, iv := range s {
		total += iv.Len()
	}
	return total
}

// ContainsSet reports whether every page of b is present in a.
func ContainsSet(a, b IntervalSet) bool {
	return EqualSet(Intersect(a, b), b)
}

// Shift adds delta to every interval boundary. It errors on overflow of the
// 48-bit page space.
func (s IntervalSet) Shift(delta uint64) (IntervalSet, error) {
	out := make(IntervalSet, 0, len(s))
	for _, iv := range s {
		start, ok1 := addU64(iv.Start, delta)
		end, ok2 := addU64(iv.End, delta)
		if !ok1 || !ok2 || end > MaxPage+1 {
			return nil, errf("shift by %d overflows the %d-bit page space", delta, PageBits)
		}
		out = append(out, Interval{start, end})
	}
	return out, nil
}

func cloneSet(s IntervalSet) IntervalSet {
	if len(s) == 0 {
		return nil
	}
	out := make(IntervalSet, len(s))
	copy(out, s)
	return out
}

func sortIntervals(s []Interval) {
	// Insertion-free sort via the standard library sort helper kept local so
	// the package has a single import surface.
	sortSlice(s)
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// addU64 returns (sum, true) on success and (_, false) on overflow.
func addU64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}
