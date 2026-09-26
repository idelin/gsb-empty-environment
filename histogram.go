// Package main implements exact merging of cumulative histograms.
//
// A Histogram is cumulative: Counts[i] is the number of observations
// with value <= Bounds[i]. Bounds are strictly increasing; Counts are
// non-decreasing. Merging is only performed when one histogram's
// boundaries can be exactly converted to the other's (the coarser
// bound set must be a subset of the finer one). Coarse buckets are
// never split into fine buckets by assumption.
package main

import (
	"errors"
	"fmt"
	"math"
)

// Histogram is a cumulative histogram with explicit upper bounds.
type Histogram struct {
	Bounds []float64 `json:"bounds"` // strictly increasing upper bounds, may end with +Inf
	Counts []int64   `json:"counts"` // cumulative counts, non-decreasing, len == len(Bounds)
}

var (
	ErrLengthMismatch = errors.New("bounds and counts length mismatch")
	ErrEmpty          = errors.New("histogram has no buckets")
	ErrBoundsOrder    = errors.New("bounds must be strictly increasing without NaN")
	ErrCountDecrease  = errors.New("cumulative counts decreased: invalid input")
	ErrCountNegative  = errors.New("cumulative counts must be non-negative")
	ErrOverflow       = errors.New("count overflow")
	ErrIncompatible   = errors.New("boundaries incompatible: neither bound set is a subset of the other")
)

// Validate checks structural invariants: matching lengths, strictly
// increasing bounds, and non-decreasing non-negative cumulative counts.
func (h Histogram) Validate() error {
	if len(h.Bounds) != len(h.Counts) {
		return fmt.Errorf("%w: %d bounds vs %d counts", ErrLengthMismatch, len(h.Bounds), len(h.Counts))
	}
	if len(h.Bounds) == 0 {
		return ErrEmpty
	}
	for i, b := range h.Bounds {
		if math.IsNaN(b) {
			return fmt.Errorf("%w: NaN at index %d", ErrBoundsOrder, i)
		}
		if i > 0 && !(b > h.Bounds[i-1]) {
			return fmt.Errorf("%w: bounds[%d]=%v not greater than bounds[%d]=%v", ErrBoundsOrder, i, b, i-1, h.Bounds[i-1])
		}
	}
	for i, c := range h.Counts {
		if c < 0 {
			return fmt.Errorf("%w: counts[%d]=%d", ErrCountNegative, i, c)
		}
		if i > 0 && h.Counts[i] < h.Counts[i-1] {
			return fmt.Errorf("%w: counts[%d]=%d < counts[%d]=%d", ErrCountDecrease, i, h.Counts[i], i-1, h.Counts[i-1])
		}
	}
	return nil
}

// Total returns the total number of observations.
func (h Histogram) Total() int64 {
	return h.Counts[len(h.Counts)-1]
}

func addCounts(a, b []int64) ([]int64, error) {
	out := make([]int64, len(a))
	for i := range a {
		if b[i] > math.MaxInt64-a[i] {
			return nil, fmt.Errorf("%w at bucket %d: %d + %d", ErrOverflow, i, a[i], b[i])
		}
		out[i] = a[i] + b[i]
	}
	return out, nil
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

// indexOfBound returns the index of bound b in h.Bounds, or -1.
func (h Histogram) indexOfBound(b float64) int {
	// Bounds are sorted; linear scan is fine for telemetry-scale data.
	for i, x := range h.Bounds {
		if x == b {
			return i
		}
	}
	return -1
}

// ConvertTo re-expresses h on the coarser bound set target. Every
// target bound must exist in h.Bounds; otherwise the conversion would
// require splitting a coarse bucket, which is refused.
func (h Histogram) ConvertTo(target []float64) (Histogram, error) {
	counts := make([]int64, len(target))
	for i, b := range target {
		j := h.indexOfBound(b)
		if j < 0 {
			return Histogram{}, fmt.Errorf("%w: target bound %v missing from source bounds", ErrIncompatible, b)
		}
		counts[i] = h.Counts[j]
	}
	return Histogram{Bounds: append([]float64(nil), target...), Counts: counts}, nil
}

// isSubset reports whether every element of sub appears in super.
func isSubset(sub, super []float64) bool {
	set := make(map[float64]struct{}, len(super))
	for _, b := range super {
		set[b] = struct{}{}
	}
	for _, b := range sub {
		if _, ok := set[b]; !ok {
			return false
		}
	}
	return true
}

// Merge combines two cumulative histograms into one.
//
//   - Identical bounds: counts are added directly.
//   - One bound set is a strict subset of the other: the finer
//     histogram is exactly converted to the coarser bounds, then added.
//   - Otherwise: ErrIncompatible; use Limits to obtain bounds on the
//     merged result instead.
func Merge(a, b Histogram) (Histogram, error) {
	if err := a.Validate(); err != nil {
		return Histogram{}, fmt.Errorf("left histogram: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Histogram{}, fmt.Errorf("right histogram: %w", err)
	}

	switch {
	case equalBounds(a.Bounds, b.Bounds):
		counts, err := addCounts(a.Counts, b.Counts)
		if err != nil {
			return Histogram{}, err
		}
		return Histogram{Bounds: append([]float64(nil), a.Bounds...), Counts: counts}, nil

	case isSubset(b.Bounds, a.Bounds): // a is finer; convert a to b's bounds
		conv, err := a.ConvertTo(b.Bounds)
		if err != nil {
			return Histogram{}, err
		}
		counts, err := addCounts(conv.Counts, b.Counts)
		if err != nil {
			return Histogram{}, err
		}
		return Histogram{Bounds: append([]float64(nil), b.Bounds...), Counts: counts}, nil

	case isSubset(a.Bounds, b.Bounds): // b is finer; convert b to a's bounds
		conv, err := b.ConvertTo(a.Bounds)
		if err != nil {
			return Histogram{}, err
		}
		counts, err := addCounts(a.Counts, conv.Counts)
		if err != nil {
			return Histogram{}, err
		}
		return Histogram{Bounds: append([]float64(nil), a.Bounds...), Counts: counts}, nil

	default:
		return Histogram{}, ErrIncompatible
	}
}

// Bound is a [min, max] interval for a cumulative count.
type Bound struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Range is the merged result expressed as per-bound intervals.
type Range struct {
	Bounds []float64 `json:"bounds"`
	Min    []int64   `json:"min"`
	Max    []int64   `json:"max"`
}

// cumulativeRange brackets the cumulative count of h at an arbitrary
// bound t without inventing data: the true value lies between the
// cumulative count at the largest known bound <= t and the smallest
// known bound >= t.
func (h Histogram) cumulativeRange(t float64) Bound {
	lo := int64(0)
	hi := h.Total()
	for i, b := range h.Bounds {
		if b <= t && h.Counts[i] > lo {
			lo = h.Counts[i]
		}
		if b >= t {
			hi = h.Counts[i]
			break
		}
	}
	return Bound{Min: lo, Max: hi}
}

// Limits computes, for each target bound, the tightest interval that
// the merged cumulative count can occupy given only the two inputs.
// It never fabricates within-bucket distributions. Returns ErrOverflow
// if interval endpoints cannot be summed safely.
func Limits(a, b Histogram, target []float64) (Range, error) {
	if err := a.Validate(); err != nil {
		return Range{}, fmt.Errorf("left histogram: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Range{}, fmt.Errorf("right histogram: %w", err)
	}
	probe := Histogram{Bounds: append([]float64(nil), target...), Counts: make([]int64, len(target))}
	if err := probe.Validate(); err != nil {
		return Range{}, fmt.Errorf("target bounds: %w", err)
	}

	r := Range{
		Bounds: append([]float64(nil), target...),
		Min:    make([]int64, len(target)),
		Max:    make([]int64, len(target)),
	}
	for i, t := range target {
		ra := a.cumulativeRange(t)
		rb := b.cumulativeRange(t)
		if rb.Min > math.MaxInt64-ra.Min || rb.Max > math.MaxInt64-ra.Max {
			return Range{}, fmt.Errorf("%w computing limits at bound %v", ErrOverflow, t)
		}
		r.Min[i] = ra.Min + rb.Min
		r.Max[i] = ra.Max + rb.Max
	}
	return r, nil
}
