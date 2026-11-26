// Package expohist provides an exponential (base-2 logarithm) histogram for
// the aggregation path.
//
// Bucket indices are derived from the observed value as
//
//	index = floor(2^scale * log2(value))
//
// where scale controls the bucket granularity. Positive and negative
// observations are encoded separately (negative values use the index of their
// absolute value) and values whose absolute value does not exceed the zero
// threshold are counted in the zero bucket.
//
// Histograms with different scales are merged by first downscaling both
// operands to the smaller scale: source indices are arithmetically right
// shifted, counts landing in the same destination bucket are summed, and the
// backing storage keeps a contiguous index range (zero-count entries preserve
// holes between occupied buckets).
package expohist

import (
	"errors"
	"math"
)

const (
	// MinScale is the smallest supported scale, matching the data model range.
	MinScale = -10
	// MaxScale is the largest supported scale, matching the data model range.
	MaxScale = 21
)

var (
	errNilReceiver = errors.New("expohist: nil histogram")
	errNilArgument = errors.New("expohist: cannot merge nil histogram")
	errScaleRange  = errors.New("expohist: scale out of [-10, 21]")
	errThreshold   = errors.New("expohist: zero threshold must be a finite non-negative number")
)

// buckets stores counts for the contiguous index range [offset,
// offset+len(counts)). Entries with a zero count represent holes inside the
// range while keeping the index representation contiguous.
type buckets struct {
	offset int
	counts []uint64
}

// add accumulates delta at idx, extending the contiguous range (and filling
// any gap with zero counts) when idx lies outside it.
func (b *buckets) add(idx int, delta uint64) {
	if len(b.counts) == 0 {
		b.offset = idx
		b.counts = []uint64{delta}
		return
	}
	last := b.offset + len(b.counts) - 1
	switch {
	case idx < b.offset:
		grown := make([]uint64, b.offset-idx+len(b.counts))
		copy(grown[b.offset-idx:], b.counts)
		b.counts = grown
		b.offset = idx
	case idx > last:
		b.counts = append(b.counts, make([]uint64, idx-last)...)
	}
	b.counts[idx-b.offset] += delta
}

// Histogram is an exponential histogram with a single scale shared by its
// positive and negative bucket ranges.
type Histogram struct {
	scale         int
	zeroThreshold float64
	zeroCount     uint64
	sum           float64
	count         uint64
	positive      buckets
	negative      buckets
}

// New creates a histogram at the given scale. zeroThreshold is the inclusive
// absolute-value threshold of the zero bucket and must be finite and
// non-negative.
func New(scale int, zeroThreshold float64) (*Histogram, error) {
	if scale < MinScale || scale > MaxScale {
		return nil, errScaleRange
	}
	if math.IsNaN(zeroThreshold) || math.IsInf(zeroThreshold, 0) || zeroThreshold < 0 {
		return nil, errThreshold
	}
	return &Histogram{scale: scale, zeroThreshold: zeroThreshold}, nil
}

// Observe records one measurement. Non-finite values are tolerated and
// counted in the zero bucket so they cannot corrupt bucket indices.
func (h *Histogram) Observe(value float64) {
	if h == nil {
		return
	}
	h.count++
	h.sum += value

	abs := value
	if value < 0 {
		abs = -value
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || abs <= h.zeroThreshold {
		h.zeroCount++
		return
	}

	idx := mapToIndex(abs, h.scale)
	if value < 0 {
		h.negative.add(idx, 1)
	} else {
		h.positive.add(idx, 1)
	}
}

// Merge combines other into h. Both histograms are first unified at the
// smaller of their scales by right-shifting bucket indices, and the merged
// zero threshold is the larger of the two. Buckets whose lower bound falls at
// or below the merged zero threshold are reclassified into the zero bucket.
func (h *Histogram) Merge(other *Histogram) error {
	if h == nil {
		return errNilReceiver
	}
	if other == nil {
		return errNilArgument
	}

	target := h.scale
	if other.scale < target {
		target = other.scale
	}
	zeroThreshold := math.Max(h.zeroThreshold, other.zeroThreshold)

	positive, z1 := downscale(h.positive, h.scale, target, zeroThreshold)
	negative, z2 := downscale(h.negative, h.scale, target, zeroThreshold)
	otherPos, z3 := downscale(other.positive, other.scale, target, zeroThreshold)
	otherNeg, z4 := downscale(other.negative, other.scale, target, zeroThreshold)
	mergeInto(&positive, otherPos)
	mergeInto(&negative, otherNeg)

	h.scale = target
	h.zeroThreshold = zeroThreshold
	h.positive = positive
	h.negative = negative
	h.zeroCount += other.zeroCount + z1 + z2 + z3 + z4
	h.sum += other.sum
	h.count += other.count
	return nil
}

// Scale returns the bucket scale.
func (h *Histogram) Scale() int { return h.scale }

// ZeroThreshold returns the inclusive absolute threshold of the zero bucket.
func (h *Histogram) ZeroThreshold() float64 { return h.zeroThreshold }

// ZeroCount returns the number of observations at or below the zero threshold.
func (h *Histogram) ZeroCount() uint64 { return h.zeroCount }

// Sum returns the arithmetic sum of all observations.
func (h *Histogram) Sum() float64 { return h.sum }

// Count returns the total number of observations.
func (h *Histogram) Count() uint64 { return h.count }

// BucketRange is a contiguous bucket range. Counts[i] is the count of bucket
// index Offset+i; a zero count marks a hole while the range stays contiguous.
type BucketRange struct {
	Offset int
	Counts []uint64
}

// Positive returns a copy of the positive bucket range.
func (h *Histogram) Positive() BucketRange { return snapshot(h.positive) }

// Negative returns a copy of the negative bucket range. Indices encode the
// absolute value of the recorded negative observations.
func (h *Histogram) Negative() BucketRange { return snapshot(h.negative) }

func snapshot(b buckets) BucketRange {
	if len(b.counts) == 0 {
		return BucketRange{}
	}
	counts := make([]uint64, len(b.counts))
	copy(counts, b.counts)
	return BucketRange{Offset: b.offset, Counts: counts}
}

// BucketBounds is the half-open decimal interval [Lower, Upper) of a bucket.
type BucketBounds struct {
	Lower float64
	Upper float64
}

// PositiveBounds maps every stored positive bucket back to its decimal
// lower and upper bounds. Adjacent entries share an endpoint, including
// across holes.
func (h *Histogram) PositiveBounds() []BucketBounds {
	positive := h.Positive()
	out := make([]BucketBounds, len(positive.Counts))
	for i := range positive.Counts {
		idx := positive.Offset + i
		out[i] = BucketBounds{
			Lower: LowerBound(h.scale, idx),
			Upper: UpperBound(h.scale, idx),
		}
	}
	return out
}

// LowerBound returns the inclusive lower decimal bound of the bucket with the
// given index at scale, i.e. 2^(index/2^scale).
func LowerBound(scale, index int) float64 {
	inverseScale := math.Ldexp(1.0, -scale) // 2^(-scale), exact for integer scale
	return math.Pow(2.0, float64(index)*inverseScale)
}

// UpperBound returns the exclusive upper decimal bound of the bucket with the
// given index at scale.
func UpperBound(scale, index int) float64 {
	return LowerBound(scale, index+1)
}

// mapToIndex computes floor(2^scale * log2(value)) for a finite positive
// value. At non-positive scales the integer exponent path is exact; at
// positive scales the logarithm path is corrected against the bucket bounds
// to stay stable at boundary values.
func mapToIndex(value float64, scale int) int {
	if scale <= 0 {
		_, exponent := math.Frexp(value)
		idx := exponent - 1 // floor(log2(value))
		if scale < 0 {
			idx >>= -scale
		}
		return idx
	}

	scaleFactor := math.Ldexp(1.0, scale) // 2^scale
	idx := int(math.Floor(math.Log2(value) * scaleFactor))
	if value < LowerBound(scale, idx) {
		idx--
	} else if value >= LowerBound(scale, idx+1) {
		idx++
	}
	return idx
}

// downscale rewrites b from scale to target (target <= scale), summing counts
// whose shifted indices coincide. Counts whose destination lower bound is at
// or below zeroThreshold are returned separately for the zero bucket.
func downscale(b buckets, scale, target int, zeroThreshold float64) (buckets, uint64) {
	var out buckets
	if len(b.counts) == 0 {
		return out, 0
	}
	shift := scale - target
	var reclassified uint64
	for i, count := range b.counts {
		if count == 0 {
			continue
		}
		idx := (b.offset + i) >> shift
		if LowerBound(target, idx) <= zeroThreshold {
			reclassified += count
			continue
		}
		out.add(idx, count)
	}
	return out, reclassified
}

func mergeInto(dst *buckets, src buckets) {
	for i, count := range src.counts {
		if count > 0 {
			dst.add(src.offset+i, count)
		}
	}
}
