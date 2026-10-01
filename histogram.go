// Package swhist implements a sliding-window histogram for streaming
// high-percentile (P90/P99) latency estimation.
//
// Design:
//   - Hybrid buck+layout: a fixed-width linear bucket region for the low
//     latency range, followed by geometric (logarithmic) buckets for the
//     long tail, giving bounded relative error across the whole range.
//   - Sliding window: a ring buffer of capacity N stores only the bucket
//     index of each sample (not the sample itself). When the window is
//     full, each Add evicts the oldest slot and decrements its bucket
//     (reverse compensation), so aggregate bucket counts always reflect
//     exactly the last N samples in O(1) per operation.
//   - Memory: O(numBuckets + capacity); raw samples are never retained.
//
// Error bound: an estimate for a value in bucket [l, u) differs from the
// true value by at most (u - l). Relative to the value itself this is
// bounded by LinearWidth/v in the linear region and by (LogRatio - 1) in
// the log region. With in-bucket linear interpolation the observed error
// on smooth distributions is far below this worst-case bound.
package swhist

import (
	"errors"
	"math"
	"sort"
)

// Config configures a Histogram.
type Config struct {
	// Capacity is the sliding-window size in samples.
	Capacity int
	// LinearMax is the upper bound of the fixed-width bucket region.
	LinearMax float64
	// LinearWidth is the fixed bucket width below LinearMax.
	LinearWidth float64
	// LogRatio is the growth ratio of geometric buckets above LinearMax
	// (e.g. 1.005 for 0.5% relative resolution).
	LogRatio float64
	// MaxValue is the upper bound of the last bucket; larger samples are
	// clamped into the overflow bucket.
	MaxValue float64
}

// DefaultConfig returns a configuration suited for millisecond-scale
// latency: 0.05ms fixed buckets up to 200ms, then 0.2% geometric buckets
// up to 10 minutes, window of 1000 samples.
func DefaultConfig() Config {
	return Config{
		Capacity:    1000,
		LinearMax:   200,
		LinearWidth: 0.05,
		LogRatio:    1.002,
		MaxValue:    600000,
	}
}

// Histogram is a sliding-window hybrid linear/log bucket histogram.
type Histogram struct {
	cfg    Config
	bounds []float64 // upper bound of each bucket, ascending
	counts []int64   // aggregate sample count per bucket
	ring   []uint32  // bucket index per window slot
	head   int       // next slot to overwrite
	size   int       // current number of samples in the window
	total  int64     // == size; kept as int64 for rank math
}

// New builds a Histogram from cfg.
func New(cfg Config) (*Histogram, error) {
	if cfg.Capacity <= 0 {
		return nil, errors.New("swhist: Capacity must be positive")
	}
	if cfg.LinearWidth <= 0 || cfg.LinearMax <= 0 {
		return nil, errors.New("swhist: LinearWidth and LinearMax must be positive")
	}
	if cfg.LogRatio <= 1 {
		return nil, errors.New("swhist: LogRatio must be > 1")
	}
	if cfg.MaxValue <= cfg.LinearMax {
		return nil, errors.New("swhist: MaxValue must exceed LinearMax")
	}
	var bounds []float64
	for b := cfg.LinearWidth; b < cfg.LinearMax; b += cfg.LinearWidth {
		bounds = append(bounds, b)
	}
	for b := cfg.LinearMax; b < cfg.MaxValue; b *= cfg.LogRatio {
		bounds = append(bounds, b)
	}
	bounds = append(bounds, cfg.MaxValue)
	return &Histogram{
		cfg:    cfg,
		bounds: bounds,
		counts: make([]int64, len(bounds)),
		ring:   make([]uint32, cfg.Capacity),
	}, nil
}

// NumBuckets returns the number of histogram buckets.
func (h *Histogram) NumBuckets() int { return len(h.bounds) }

// Size returns the current number of samples in the window.
func (h *Histogram) Size() int { return h.size }

// bucketIndex returns the index of the bucket holding v.
func (h *Histogram) bucketIndex(v float64) int {
	i := sort.Search(len(h.bounds), func(i int) bool { return v < h.bounds[i] })
	if i == len(h.bounds) {
		return len(h.bounds) - 1 // clamp overflow into the top bucket
	}
	return i
}

// Add inserts one sample into the window, evicting and
// reverse-compensating the oldest sample when the window is full.
func (h *Histogram) Add(v float64) {
	if v < 0 {
		v = 0
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	idx := uint32(h.bucketIndex(v))
	if h.size == h.cfg.Capacity {
		old := h.ring[h.head]
		h.counts[old]--
		h.size--
		h.total--
	}
	h.ring[h.head] = idx
	h.counts[idx]++
	h.size++
	h.total++
	h.head = (h.head + 1) % h.cfg.Capacity
}

// Quantile estimates the q-quantile (0 < q <= 1) of the samples currently
// in the window, using nearest-rank on bucket counts plus linear
// interpolation inside the selected bucket. Returns 0 when empty.
func (h *Histogram) Quantile(q float64) float64 {
	if h.total == 0 {
		return 0
	}
	if q <= 0 {
		q = 0.0000001
	}
	if q > 1 {
		q = 1
	}
	rank := int64(math.Ceil(q * float64(h.total)))
	if rank < 1 {
		rank = 1
	}
	var cum int64
	for i, c := range h.counts {
		if cum+c >= rank {
			lower := 0.0
			if i > 0 {
				lower = h.bounds[i-1]
			}
			upper := h.bounds[i]
			// k-th sample (1-based) among c uniformly spread in [lower, upper).
			k := rank - cum
			return lower + (float64(k)-0.5)/float64(c)*(upper-lower)
		}
		cum += c
	}
	return h.bounds[len(h.bounds)-1]
}

// P90 returns the estimated 90th percentile.
func (h *Histogram) P90() float64 { return h.Quantile(0.90) }

// P99 returns the estimated 99th percentile.
func (h *Histogram) P99() float64 { return h.Quantile(0.99) }

// Count returns the aggregate count of the bucket containing v.
// Exposed for verification of reverse compensation.
func (h *Histogram) BucketCount(v float64) int64 {
	return h.counts[h.bucketIndex(v)]
}

// Sum returns the total number of samples across all buckets.
func (h *Histogram) Sum() int64 {
	var s int64
	for _, c := range h.counts {
		s += c
	}
	return s
}
