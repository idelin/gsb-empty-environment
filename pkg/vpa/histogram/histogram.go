package histogram

import "sort"

// PercentileMode selects how percentile estimates are reported.
type PercentileMode int

const (
	// Linear interpolates inside the bucket that contains the requested
	// quantile, using the cumulative weight at both bucket edges.
	Linear PercentileMode = iota
	// UpperBound reports the upper edge of the containing bucket without
	// interpolation, yielding a conservative over-estimate.
	UpperBound
)

// Histogram is a geometric-bucket histogram with plain (non-decaying) weights.
// It is the weight storage primitive reused by DecayingHistogram.
type Histogram struct {
	options BucketOptions
	buckets map[int]float64
	total   float64
}

// NewHistogram creates an empty histogram using the given bucket layout.
func NewHistogram(options BucketOptions) *Histogram {
	return &Histogram{
		options: options,
		buckets: make(map[int]float64),
	}
}

// Options returns the bucket layout.
func (h *Histogram) Options() BucketOptions { return h.options }

// AddSample accumulates weight into the bucket that owns value.
func (h *Histogram) AddSample(value, weight float64) {
	if weight <= 0 {
		return
	}
	index := bucketIndex(value, h.options)
	h.addToBucket(index, weight)
}

// addToBucket accumulates an already-scaled weight directly into bucket.
func (h *Histogram) addToBucket(index int, weight float64) {
	h.buckets[index] += weight
	h.total += weight
}

// Scale multiplies every bucket weight and the total weight by factor. A
// factor of zero effectively empties the histogram.
func (h *Histogram) Scale(factor float64) {
	if factor == 1 {
		return
	}
	for index, weight := range h.buckets {
		h.buckets[index] = weight * factor
	}
	h.total *= factor
}

// TotalWeight returns the sum of all bucket weights.
func (h *Histogram) TotalWeight() float64 { return h.total }

// BucketWeight returns the weight stored in a specific bucket.
func (h *Histogram) BucketWeight(index int) float64 { return h.buckets[index] }

// IsEmpty reports whether the histogram carries any weight.
func (h *Histogram) IsEmpty() bool { return h.total == 0 }

// Percentile returns the value at quantile p in [0, 1]. The zero-quantile
// resolves to zero and the unit quantile to the highest occupied upper edge.
func (h *Histogram) Percentile(p float64, mode PercentileMode) float64 {
	if h.IsEmpty() {
		return 0
	}
	if p <= 0 {
		return 0
	}

	indices := h.sortedIndices()
	highest := indices[len(indices)-1]
	if p >= 1 {
		return bucketUpperBound(highest, h.options)
	}

	target := h.total * p
	cumulative := 0.0
	for _, index := range indices {
		weight := h.buckets[index]
		upper := bucketUpperBound(index, h.options)
		if cumulative+weight >= target {
			if mode == UpperBound {
				return upper
			}
			lower := bucketLowerBound(index, h.options)
			// Fraction of the target bucket that must be traversed.
			inner := 0.0
			if weight > 0 {
				inner = (target - cumulative) / weight
			}
			return lower + (upper-lower)*inner
		}
		cumulative += weight
	}
	return bucketUpperBound(highest, h.options)
}

func (h *Histogram) sortedIndices() []int {
	indices := make([]int, 0, len(h.buckets))
	for index, weight := range h.buckets {
		if weight != 0 {
			indices = append(indices, index)
		}
	}
	sort.Ints(indices)
	return indices
}
