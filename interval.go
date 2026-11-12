package netsec

import (
	"encoding/json"
	"math"
	"sort"
)

// Interval is a closed integer interval [Low, High].
// Both endpoints are inclusive and Low must be less than or equal to High.
type Interval struct {
	Low  int64 `json:"low"`
	High int64 `json:"high"`
}

// IntervalSet is a normalized, disjoint set of closed integer intervals.
//
// The zero value is the empty set. Normalized means the stored intervals are
// sorted by Low, never overlap, and never touch (adjacent intervals are merged
// into one), which keeps union, intersection and subtraction deterministic.
type IntervalSet struct {
	ranges []Interval
}

// NewIntervalSet builds a normalized IntervalSet from arbitrary intervals.
// Invalid intervals (Low > High) are ignored so that callers can pass through
// half-built conditions without panicking; use Validate on statements when
// strict input checking is required.
func NewIntervalSet(ivs ...Interval) IntervalSet {
	valid := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if iv.Low <= iv.High {
			valid = append(valid, iv)
		}
	}
	return normalize(valid)
}

// Empty reports whether the set contains no resource id.
func (s IntervalSet) Empty() bool {
	return len(s.ranges) == 0
}

// Ranges returns the normalized intervals. Callers must not mutate the slice.
func (s IntervalSet) Ranges() []Interval {
	return s.ranges
}

// Contains reports whether resource id v is covered by the set.
func (s IntervalSet) Contains(v int64) bool {
	idx := sort.Search(len(s.ranges), func(i int) bool {
		return s.ranges[i].High >= v
	})
	return idx < len(s.ranges) && s.ranges[idx].Low <= v && v <= s.ranges[idx].High
}

// Equal reports whether two sets describe exactly the same resources.
func (s IntervalSet) Equal(other IntervalSet) bool {
	if len(s.ranges) != len(other.ranges) {
		return false
	}
	for i := range s.ranges {
		if s.ranges[i] != other.ranges[i] {
			return false
		}
	}
	return true
}

// Union returns the union of the two sets.
func (s IntervalSet) Union(other IntervalSet) IntervalSet {
	combined := make([]Interval, 0, len(s.ranges)+len(other.ranges))
	combined = append(combined, s.ranges...)
	combined = append(combined, other.ranges...)
	return normalize(combined)
}

// Intersect returns the resources present in both sets.
func (s IntervalSet) Intersect(other IntervalSet) IntervalSet {
	var out []Interval
	left, right := s.ranges, other.ranges
	for i, j := 0, 0; i < len(left) && j < len(right); {
		lo := maxI(left[i].Low, right[j].Low)
		hi := minI(left[i].High, right[j].High)
		if lo <= hi {
			out = append(out, Interval{Low: lo, High: hi})
		}
		if left[i].High < right[j].High {
			i++
		} else {
			j++
		}
	}
	return IntervalSet{ranges: out}
}

// Subtract returns the resources in s that are not in other.
func (s IntervalSet) Subtract(other IntervalSet) IntervalSet {
	var out []Interval
	for _, iv := range s.ranges {
		pieces := []Interval{iv}
		for _, cut := range other.ranges {
			if cut.High < iv.Low || cut.Low > iv.High {
				continue
			}
			var next []Interval
			for _, piece := range pieces {
				if cut.High < piece.Low || cut.Low > piece.High {
					next = append(next, piece)
					continue
				}
				if piece.Low < cut.Low {
					next = append(next, Interval{Low: piece.Low, High: cut.Low - 1})
				}
				if cut.High < piece.High && cut.High < math.MaxInt64 {
					next = append(next, Interval{Low: cut.High + 1, High: piece.High})
				}
			}
			pieces = next
		}
		out = append(out, pieces...)
	}
	return normalize(out)
}

// MarshalJSON emits the normalized intervals so snapshots are stable.
func (s IntervalSet) MarshalJSON() ([]byte, error) {
	if len(s.ranges) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(s.ranges)
}

// UnmarshalJSON normalizes incoming intervals, accepting unsorted input.
func (s *IntervalSet) UnmarshalJSON(data []byte) error {
	var ivs []Interval
	if err := json.Unmarshal(data, &ivs); err != nil {
		return err
	}
	*s = normalize(ivs)
	return nil
}

func normalize(ivs []Interval) IntervalSet {
	if len(ivs) == 0 {
		return IntervalSet{}
	}
	sorted := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if iv.Low <= iv.High {
			sorted = append(sorted, iv)
		}
	}
	if len(sorted) == 0 {
		return IntervalSet{}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Low != sorted[j].Low {
			return sorted[i].Low < sorted[j].Low
		}
		return sorted[i].High < sorted[j].High
	})

	merged := make([]Interval, 0, len(sorted))
	cur := sorted[0]
	for _, iv := range sorted[1:] {
		switch {
		case iv.Low <= cur.High:
			if iv.High > cur.High {
				cur.High = iv.High
			}
		case iv.Low == cur.High+1 && cur.High < math.MaxInt64:
			cur.High = iv.High
		default:
			merged = append(merged, cur)
			cur = iv
		}
	}
	merged = append(merged, cur)
	return IntervalSet{ranges: merged}
}

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
