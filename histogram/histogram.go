// Package histogram provides cumulative histograms that can be merged
// across different boundary sets by projecting onto common boundaries.
package histogram

import (
	"errors"
	"fmt"
	"sort"
)

// Histogram is a cumulative histogram.
//
// Boundaries are sorted ascending and unique. Cumulative has
// len(Boundaries)+1 entries: Cumulative[i] is the number of observed
// samples with value <= Boundaries[i], and the last entry is the total
// number of samples. Cumulative is therefore non-decreasing and its
// final entry always equals Total.
type Histogram struct {
	Boundaries []float64
	Cumulative []uint64
	Total      uint64
}

// New builds a cumulative histogram from raw samples and the given
// boundary set. Boundaries are normalized (sorted and deduplicated).
func New(boundaries []float64, samples []float64) *Histogram {
	b := normalize(boundaries)
	h := &Histogram{
		Boundaries: b,
		Cumulative: make([]uint64, len(b)+1),
		Total:      uint64(len(samples)),
	}
	for _, s := range samples {
		// First bucket whose boundary is >= s gets the cumulative bump.
		i := sort.SearchFloat64s(b, s)
		for j := i; j < len(b); j++ {
			h.Cumulative[j]++
		}
	}
	// The final slot counts every sample.
	h.Cumulative[len(b)] = h.Total
	return h
}

// Merge combines two cumulative histograms. Because exact per-bucket
// counts for boundaries that are not present in a histogram cannot be
// recovered without interpolation, the result is defined only on the
// intersection of both boundary sets: every boundary in the result
// exists in both inputs, so its cumulative count is exact. If the
// boundary sets are disjoint, the result carries only the total.
func Merge(a, b *Histogram) *Histogram {
	common := intersect(a.Boundaries, b.Boundaries)
	merged := &Histogram{
		Boundaries: common,
		Cumulative: make([]uint64, len(common)+1),
		Total:      a.Total + b.Total,
	}
	for i, boundary := range common {
		merged.Cumulative[i] = a.countAt(boundary) + b.countAt(boundary)
	}
	merged.Cumulative[len(common)] = merged.Total
	return merged
}

// Project returns the histogram restricted to the given boundaries.
// Every requested boundary must exist in h; projecting onto a boundary
// the histogram does not have would require interpolating counts, which
// this library deliberately refuses to do.
func (h *Histogram) Project(boundaries []float64) (*Histogram, error) {
	b := normalize(boundaries)
	p := &Histogram{
		Boundaries: b,
		Cumulative: make([]uint64, len(b)+1),
		Total:      h.Total,
	}
	for i, boundary := range b {
		idx := sort.SearchFloat64s(h.Boundaries, boundary)
		if idx >= len(h.Boundaries) || h.Boundaries[idx] != boundary {
			return nil, fmt.Errorf("histogram: boundary %v not present, cannot project without interpolation", boundary)
		}
		p.Cumulative[i] = h.Cumulative[idx]
	}
	p.Cumulative[len(b)] = h.Total
	return p, nil
}

// Validate checks the cumulative invariants: counts are non-decreasing
// and the final cumulative count equals the total.
func (h *Histogram) Validate() error {
	if len(h.Cumulative) != len(h.Boundaries)+1 {
		return errors.New("histogram: cumulative length must be len(boundaries)+1")
	}
	for i := 1; i < len(h.Cumulative); i++ {
		if h.Cumulative[i] < h.Cumulative[i-1] {
			return fmt.Errorf("histogram: cumulative counts not monotonic at index %d", i)
		}
	}
	if h.Cumulative[len(h.Cumulative)-1] != h.Total {
		return fmt.Errorf("histogram: final cumulative count %d != total %d",
			h.Cumulative[len(h.Cumulative)-1], h.Total)
	}
	return nil
}

// countAt returns the exact cumulative count at a boundary that is
// guaranteed to exist in h. Callers (Merge) only pass common boundaries.
func (h *Histogram) countAt(boundary float64) uint64 {
	idx := sort.SearchFloat64s(h.Boundaries, boundary)
	if idx >= len(h.Boundaries) || h.Boundaries[idx] != boundary {
		panic(fmt.Sprintf("histogram: boundary %v not present", boundary))
	}
	return h.Cumulative[idx]
}

// normalize sorts boundaries ascending and removes duplicates.
func normalize(boundaries []float64) []float64 {
	b := make([]float64, len(boundaries))
	copy(b, boundaries)
	sort.Float64s(b)
	out := b[:0]
	for i, v := range b {
		if i == 0 || v != b[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// intersect returns the sorted set of boundaries present in both a and b.
func intersect(a, b []float64) []float64 {
	var common []float64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			common = append(common, a[i])
			i++
			j++
		}
	}
	return common
}
