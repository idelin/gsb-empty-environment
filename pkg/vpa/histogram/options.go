package histogram

import "math"

// BucketOptions describes the geometric bucket layout shared by every
// histogram implementation in this package.
//
// Buckets grow exponentially: bucket 0 covers [0, firstBucketSize), and every
// subsequent bucket is ratio times wider than its predecessor. Values up to
// epsilon are treated as zero so that tiny, noise-level samples never fan out
// into an unbounded number of negative-indexed buckets.
type BucketOptions interface {
	// FirstBucketSize returns the width of the first (index 0) bucket.
	FirstBucketSize() float64
	// Ratio returns the geometric growth factor (strictly greater than 1).
	Ratio() float64
	// Epsilon returns the threshold below which a value is treated as zero.
	Epsilon() float64
}

// ExponentialBucketOptions is the standard BucketOptions implementation.
type ExponentialBucketOptions struct {
	firstBucketSize float64
	ratio           float64
	epsilon         float64
}

// NewExponentialBucketOptions builds bucket options, deriving epsilon as a
// fixed tiny fraction of the first bucket width when the caller does not need
// to control the zero threshold explicitly.
func NewExponentialBucketOptions(firstBucketSize, ratio float64) ExponentialBucketOptions {
	if firstBucketSize <= 0 {
		panic("histogram: firstBucketSize must be positive")
	}
	if ratio <= 1 {
		panic("histogram: ratio must be greater than 1")
	}
	return ExponentialBucketOptions{
		firstBucketSize: firstBucketSize,
		ratio:           ratio,
		epsilon:         epsilonFromFirstBucketSize(firstBucketSize),
	}
}

// NewExponentialBucketOptionsWithEpsilon allows callers to override epsilon.
func NewExponentialBucketOptionsWithEpsilon(firstBucketSize, ratio, epsilon float64) ExponentialBucketOptions {
	if epsilon <= 0 || epsilon >= firstBucketSize {
		panic("histogram: epsilon must be in (0, firstBucketSize)")
	}
	options := NewExponentialBucketOptions(firstBucketSize, ratio)
	options.epsilon = epsilon
	return options
}

func (o ExponentialBucketOptions) FirstBucketSize() float64 { return o.firstBucketSize }
func (o ExponentialBucketOptions) Ratio() float64           { return o.ratio }
func (o ExponentialBucketOptions) Epsilon() float64         { return o.epsilon }

// epsilonFromFirstBucketSize mirrors the upstream VPA convention: values
// smaller than 1e-12 of the first bucket width collapse into the zero bucket.
func epsilonFromFirstBucketSize(firstBucketSize float64) float64 {
	return 1e-12 * firstBucketSize
}

// bucketIndex returns the geometric bucket that owns value.
func bucketIndex(value float64, options BucketOptions) int {
	if value < options.Epsilon() {
		return 0
	}
	// Bucket i spans [firstBucketSize*ratio^(i-1), firstBucketSize*ratio^i).
	index := math.Floor(math.Log(value/options.FirstBucketSize())/math.Log(options.Ratio())) + 1
	return int(index)
}

// bucketUpperBound returns the (exclusive) upper edge of bucket index.
func bucketUpperBound(index int, options BucketOptions) float64 {
	if index <= 0 {
		return options.FirstBucketSize()
	}
	return options.FirstBucketSize() * math.Pow(options.Ratio(), float64(index))
}

// bucketLowerBound returns the (inclusive) lower edge of bucket index.
func bucketLowerBound(index int, options BucketOptions) float64 {
	if index <= 0 {
		return 0
	}
	return options.FirstBucketSize() * math.Pow(options.Ratio(), float64(index-1))
}
