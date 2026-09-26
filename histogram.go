// Package histmerge merges cumulative histograms whose bucket boundaries
// differ, but only when the conversion is exact. Coarse buckets are never
// split into finer ones; identical boundaries are summed directly.
package histmerge

import (
	"fmt"
	"math"
	"sort"
)

// Histogram is a cumulative histogram: Counts[i] is the number of
// observations less than or equal to Bounds[i]. Bounds must be strictly
// increasing and Counts must be non-negative and non-decreasing.
type Histogram struct {
	Bounds []float64
	Counts []int64
}

// New returns an empty cumulative histogram with the given bounds.
func New(bounds []float64) *Histogram {
	return &Histogram{
		Bounds: append([]float64(nil), bounds...),
		Counts: make([]int64, len(bounds)),
	}
}

// Validate checks structural invariants: matching lengths, finite strictly
// increasing bounds, and non-negative non-decreasing cumulative counts.
// A decrease in cumulative counts is reported as an input error.
func (h *Histogram) Validate() error {
	if len(h.Bounds) != len(h.Counts) {
		return fmt.Errorf("bounds/counts length mismatch: %d bounds vs %d counts",
			len(h.Bounds), len(h.Counts))
	}
	for i, b := range h.Bounds {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			return fmt.Errorf("bound at index %d is not finite: %v", i, b)
		}
		if i > 0 && b <= h.Bounds[i-1] {
			return fmt.Errorf("bounds not strictly increasing at index %d: %v then %v",
				i, h.Bounds[i-1], b)
		}
	}
	for i, c := range h.Counts {
		if c < 0 {
			return fmt.Errorf("negative cumulative count at index %d: %d", i, c)
		}
		if i > 0 && c < h.Counts[i-1] {
			return fmt.Errorf("cumulative counts decreased at index %d: %d < %d",
				i, c, h.Counts[i-1])
		}
	}
	return nil
}

// Observe records one observation. It returns false (and records nothing)
// when v exceeds the last bound.
func (h *Histogram) Observe(v float64) bool {
	i := sort.Search(len(h.Bounds), func(i int) bool { return v <= h.Bounds[i] })
	if i == len(h.Bounds) {
		return false
	}
	for j := i; j < len(h.Counts); j++ {
		h.Counts[j]++
	}
	return true
}

func (h *Histogram) clone() *Histogram {
	return &Histogram{
		Bounds: append([]float64(nil), h.Bounds...),
		Counts: append([]int64(nil), h.Counts...),
	}
}

func equalBounds(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isSubset reports whether every bound in sub appears in super.
// Both slices must be sorted ascending.
func isSubset(sub, super []float64) bool {
	j := 0
	for _, v := range sub {
		found := false
		for j < len(super) {
			if super[j] == v {
				found = true
				j++
				break
			}
			if super[j] > v {
				return false
			}
			j++
		}
		if !found {
			return false
		}
	}
	return true
}

// Project converts h to a coarser bound set. target must be a subset of
// h.Bounds; the cumulative count at each kept bound is preserved exactly.
func (h *Histogram) Project(target []float64) (*Histogram, error) {
	if !isSubset(target, h.Bounds) {
		return nil, fmt.Errorf("cannot project: target bounds %v are not a subset of source bounds %v",
			target, h.Bounds)
	}
	out := &Histogram{
		Bounds: append([]float64(nil), target...),
		Counts: make([]int64, len(target)),
	}
	j := 0
	for i, b := range target {
		for h.Bounds[j] != b {
			j++
		}
		out.Counts[i] = h.Counts[j]
		j++
	}
	return out, nil
}

// add sums other into h in place, checking for int64 overflow.
// Both histograms must share identical bounds.
func (h *Histogram) add(other *Histogram) error {
	if !equalBounds(h.Bounds, other.Bounds) {
		return fmt.Errorf("cannot add: bounds differ: %v vs %v", h.Bounds, other.Bounds)
	}
	for i := range h.Counts {
		if other.Counts[i] > math.MaxInt64-h.Counts[i] {
			return fmt.Errorf("count overflow at index %d: %d + %d exceeds int64",
				i, h.Counts[i], other.Counts[i])
		}
		h.Counts[i] += other.Counts[i]
	}
	return nil
}

// Merge combines two cumulative histograms into a new one.
//
//   - Identical bounds: counts are added directly.
//   - One bound set is a subset of the other: the finer histogram is
//     projected onto the coarser bounds (exact for cumulative counts),
//     then counts are added. Coarse buckets are never split into finer
//     ones, because that cannot be done exactly.
//   - Otherwise the merge is rejected and both bound sets are reported.
func Merge(a, b *Histogram) (*Histogram, error) {
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("left input invalid: %w", err)
	}
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("right input invalid: %w", err)
	}
	switch {
	case equalBounds(a.Bounds, b.Bounds):
		out := a.clone()
		if err := out.add(b); err != nil {
			return nil, err
		}
		return out, nil
	case isSubset(a.Bounds, b.Bounds):
		projected, err := b.Project(a.Bounds)
		if err != nil {
			return nil, err
		}
		out := a.clone()
		if err := out.add(projected); err != nil {
			return nil, err
		}
		return out, nil
	case isSubset(b.Bounds, a.Bounds):
		projected, err := a.Project(b.Bounds)
		if err != nil {
			return nil, err
		}
		if err := projected.add(b); err != nil {
			return nil, err
		}
		return projected, nil
	default:
		return nil, fmt.Errorf("cannot merge: incompatible bounds %v vs %v: "+
			"neither is a subset of the other and coarse buckets are never split",
			a.Bounds, b.Bounds)
	}
}
