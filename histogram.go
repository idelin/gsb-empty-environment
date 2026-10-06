// Package metrics provides cumulative histograms with explicit bucket
// boundaries and exact, interpolation-free merging.
package metrics

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Histogram is a cumulative histogram over explicit upper bounds.
//
// Bounds holds sorted, strictly increasing upper bounds (the "le" values),
// excluding the implicit +Inf bucket. Cum[i] is the number of observations
// less than or equal to Bounds[i]. Total is the total number of
// observations, i.e. the cumulative count at +Inf.
type Histogram struct {
	Bounds []float64
	Cum    []uint64
	Total  uint64
}

// NewFromSamples builds a cumulative histogram for the given bounds from raw
// samples. Bounds are copied, sorted and deduplicated.
func NewFromSamples(bounds []float64, samples []float64) *Histogram {
	b := normalizeBounds(bounds)
	s := make([]float64, len(samples))
	copy(s, samples)
	sort.Float64s(s)

	h := &Histogram{Bounds: b, Cum: make([]uint64, len(b)), Total: uint64(len(s))}
	for i, le := range b {
		h.Cum[i] = uint64(sort.Search(len(s), func(j int) bool { return s[j] > le }))
	}
	return h
}

// normalizeBounds returns a sorted copy of bounds with duplicates and NaN
// removed. +Inf is dropped: it is always implicit as the terminal bucket.
func normalizeBounds(bounds []float64) []float64 {
	b := make([]float64, 0, len(bounds))
	for _, le := range bounds {
		if math.IsNaN(le) || math.IsInf(le, 1) {
			continue
		}
		b = append(b, le)
	}
	sort.Float64s(b)
	out := b[:0]
	for i, le := range b {
		if i > 0 && le == b[i-1] {
			continue
		}
		out = append(out, le)
	}
	return out
}

// cumAt returns the exact cumulative count at bound le. The second return
// value is false when le is not one of the histogram's explicit bounds; no
// interpolation is ever performed.
func (h *Histogram) cumAt(le float64) (uint64, bool) {
	i := sort.Search(len(h.Bounds), func(i int) bool { return h.Bounds[i] >= le })
	if i < len(h.Bounds) && h.Bounds[i] == le {
		return h.Cum[i], true
	}
	return 0, false
}

// CumAt exposes the exact cumulative count at an explicit bound, or false if
// the bound is not present in this histogram.
func (h *Histogram) CumAt(le float64) (uint64, bool) { return h.cumAt(le) }

// Merge combines two cumulative histograms into one. When the boundary sets
// differ, both are projected onto their common boundaries (the intersection):
// a cumulative count at a shared bound is exact in both inputs, so the merged
// count is exact as well. Bounds present in only one input are dropped rather
// than fabricated by interpolation. The merged Total is the sum of both
// totals. If the boundary intersection is empty, the result carries only the
// total count.
func Merge(a, b *Histogram) *Histogram {
	common := intersectBounds(a.Bounds, b.Bounds)
	m := &Histogram{Bounds: common, Cum: make([]uint64, len(common)), Total: a.Total + b.Total}
	for i, le := range common {
		ca, _ := a.cumAt(le)
		cb, _ := b.cumAt(le)
		m.Cum[i] = ca + cb
	}
	return m
}

// intersectBounds returns the sorted intersection of two sorted bound slices.
func intersectBounds(a, b []float64) []float64 {
	var out []float64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// Validate checks the histogram invariants: bounds strictly increasing,
// cumulative counts monotonically non-decreasing, and the cumulative series
// terminating exactly at Total (the implicit +Inf bucket).
func (h *Histogram) Validate() error {
	if len(h.Bounds) != len(h.Cum) {
		return fmt.Errorf("bounds/cum length mismatch: %d != %d", len(h.Bounds), len(h.Cum))
	}
	prev := uint64(0)
	for i, le := range h.Bounds {
		if math.IsNaN(le) || math.IsInf(le, 0) {
			return fmt.Errorf("bound %d is not finite: %v", i, le)
		}
		if i > 0 && le <= h.Bounds[i-1] {
			return fmt.Errorf("bounds not strictly increasing at %d: %v <= %v", i, le, h.Bounds[i-1])
		}
		if h.Cum[i] < prev {
			return fmt.Errorf("cumulative counts not monotonic at bound %v: %d < %d", le, h.Cum[i], prev)
		}
		prev = h.Cum[i]
	}
	if prev > h.Total {
		return fmt.Errorf("final cumulative count %d exceeds total %d", prev, h.Total)
	}
	return nil
}

// Equals reports whether two histograms have identical bounds, cumulative
// counts and totals.
func (h *Histogram) Equals(o *Histogram) bool {
	if h.Total != o.Total || len(h.Bounds) != len(o.Bounds) {
		return false
	}
	for i := range h.Bounds {
		if h.Bounds[i] != o.Bounds[i] || h.Cum[i] != o.Cum[i] {
			return false
		}
	}
	return true
}

var errBatchExists = errors.New("metrics: batch already exists")

// ErrBatchExists is returned when adding a batch under an id that is already
// registered.
func ErrBatchExists() error { return errBatchExists }
