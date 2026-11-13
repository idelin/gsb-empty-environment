package netsec

import (
	"fmt"
	"math/big"
	"sort"
)

// Interval is a non-empty inclusive integer interval [Lo, Hi].  Endpoints are
// always exact big integers.
type Interval struct {
	Lo *big.Int `json:"lo"`
	Hi *big.Int `json:"hi"`
}

// CandidateInterval is an alias for Interval.
type CandidateInterval = Interval

// NewInterval validates and copies an inclusive interval.
func NewInterval(lo, hi *big.Int) (Interval, error) {
	if lo == nil || hi == nil || lo.Cmp(hi) > 0 {
		return Interval{}, ErrInvalidInterval
	}
	return Interval{Lo: new(big.Int).Set(lo), Hi: new(big.Int).Set(hi)}, nil
}

// Clone returns a deep copy.
func (iv Interval) Clone() Interval {
	return Interval{Lo: new(big.Int).Set(iv.Lo), Hi: new(big.Int).Set(iv.Hi)}
}

// Contains reports whether x is in the inclusive interval.
func (iv Interval) Contains(x *big.Int) bool {
	return iv.Lo != nil && iv.Hi != nil && x != nil && iv.Lo.Cmp(x) <= 0 && x.Cmp(iv.Hi) <= 0
}

// Size returns Hi-Lo+1 as an exact integer.
func (iv Interval) Size() *big.Int {
	return new(big.Int).Add(new(big.Int).Sub(iv.Hi, iv.Lo), big.NewInt(1))
}

// Single reports whether the interval contains exactly one integer.
func (iv Interval) Single() bool { return iv.Lo != nil && iv.Hi != nil && iv.Lo.Cmp(iv.Hi) == 0 }

func (iv Interval) String() string {
	if iv.Lo == nil || iv.Hi == nil {
		return "<invalid>"
	}
	if iv.Lo.Cmp(iv.Hi) == 0 {
		return "[" + iv.Lo.String() + "]"
	}
	return "[" + iv.Lo.String() + "," + iv.Hi.String() + "]"
}

// CloneIntervals returns a deep copy of intervals.
func CloneIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return []Interval{}
	}
	out := make([]Interval, len(intervals))
	for i := range intervals {
		out[i] = intervals[i].Clone()
	}
	return out
}

// MergeIntervals sorts intervals, drops duplicates, and merges overlapping or
// adjacent intervals.  Adjacent integer intervals such as [a,b] and [b+1,c]
// become one interval [a,c].
func MergeIntervals(intervals []Interval) ([]Interval, error) {
	for i := range intervals {
		if intervals[i].Lo == nil || intervals[i].Hi == nil || intervals[i].Lo.Cmp(intervals[i].Hi) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInterval, intervals[i])
		}
	}

	sorted := CloneIntervals(intervals)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Lo.Cmp(sorted[j].Lo) < 0
	})
	if len(sorted) == 0 {
		return []Interval{}, nil
	}

	out := make([]Interval, 0, len(sorted))
	for _, iv := range sorted {
		if len(out) == 0 {
			out = append(out, iv.Clone())
			continue
		}
		last := &out[len(out)-1]
		nextAfterLast := new(big.Int).Add(last.Hi, big.NewInt(1))
		if iv.Lo.Cmp(nextAfterLast) <= 0 {
			if iv.Hi.Cmp(last.Hi) > 0 {
				last.Hi = new(big.Int).Set(iv.Hi)
			}
			continue
		}
		out = append(out, iv.Clone())
	}
	return out, nil
}

// TotalSize counts the integers represented by the union of intervals.
func TotalSize(intervals []Interval) *big.Int {
	merged, err := MergeIntervals(intervals)
	if err != nil {
		return nil
	}
	total := new(big.Int)
	for _, iv := range merged {
		total.Add(total, iv.Size())
	}
	return total
}

// FirstCandidate returns the smallest candidate or nil for an empty set.
func FirstCandidate(intervals []Interval) *big.Int {
	var best *big.Int
	for _, iv := range intervals {
		if iv.Lo == nil {
			continue
		}
		if best == nil || iv.Lo.Cmp(best) < 0 {
			best = new(big.Int).Set(iv.Lo)
		}
	}
	return best
}
