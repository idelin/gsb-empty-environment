// Package estimator turns decaying usage histograms into VPA resource
// recommendations: a target quantile plus lower/upper confidence bounds that
// widen when history is short or scarce, and a ContainerResourcePolicy that
// clamps the result to minAllowed/maxAllowed while preserving an uncapped
// target for diagnostics.
package estimator

import (
	"math"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
)

// Standard quantiles used by the VPA recommender.
const (
	Percentile50  = 0.5
	Percentile90  = 0.9
	Percentile95  = 0.95
	Percentile100 = 1.0
)

// Confidence margin tuning. With one sample the margin starts near
// maxMargin (a very wide interval) and decays toward minMargin as more
// samples accumulate over longer time spans.
const (
	maxMargin           = 0.5
	minMargin           = 0.01
	sampleDecayFactor   = 0.15
	fullSpanWeightHours = 24.0
	maxSpanWeight       = 6.0
)

// Estimate is the recommendation for a single resource before policy
// clamping. All values use the same unit as the ingested samples.
type Estimate struct {
	LowerBound     float64
	Target         float64
	UncappedTarget float64
	UpperBound     float64
}

// Estimator owns one decaying histogram plus the bookkeeping required to
// derive a confidence interval: the number of samples and the time span they
// cover.
type Estimator struct {
	histogram   *histogram.DecayingHistogram
	percentile  float64
	mode        histogram.PercentileMode
	sampleCount int
	firstSample time.Time
	lastSample  time.Time
}

// NewEstimator builds an Estimator around an existing decaying histogram.
func NewEstimator(h *histogram.DecayingHistogram, percentile float64, mode histogram.PercentileMode) *Estimator {
	return &Estimator{
		histogram:  h,
		percentile: percentile,
		mode:       mode,
	}
}

// AddSample forwards an observation to the decaying histogram and updates the
// sample-count/span bookkeeping.
func (e *Estimator) AddSample(value, weight float64, timestamp time.Time) {
	if e.sampleCount == 0 || timestamp.Before(e.firstSample) {
		e.firstSample = timestamp
	}
	if e.sampleCount == 0 || timestamp.After(e.lastSample) {
		e.lastSample = timestamp
	}
	e.sampleCount++
	e.histogram.AddSample(value, weight, timestamp)
}

// SampleCount returns the number of ingested samples.
func (e *Estimator) SampleCount() int { return e.sampleCount }

// Span returns the time between the first and last sample.
func (e *Estimator) Span() time.Duration {
	if e.sampleCount == 0 {
		return 0
	}
	return e.lastSample.Sub(e.firstSample)
}

// Histogram exposes the underlying decaying histogram.
func (e *Estimator) Histogram() *histogram.DecayingHistogram { return e.histogram }

// confidenceMargin implements a margin that shrinks with both sample count
// and covered time span, guaranteeing wider intervals for fewer samples.
func confidenceMargin(sampleCount int, span time.Duration) float64 {
	if sampleCount <= 0 {
		return maxMargin
	}
	spanWeight := 1 + math.Min(maxSpanWeight, span.Hours()/fullSpanWeightHours)
	exponent := sampleDecayFactor * float64(sampleCount) * spanWeight
	margin := maxMargin * math.Exp(-exponent)
	return math.Max(minMargin, margin)
}

// Estimate computes lower/target/upper values. UncappedTarget equals Target
// at this stage; policy clamping later mutates the others while preserving it.
func (e *Estimator) Estimate() Estimate {
	if e.sampleCount == 0 || e.histogram.IsEmpty() {
		return Estimate{}
	}
	target := e.histogram.Percentile(e.percentile, e.mode)
	margin := confidenceMargin(e.sampleCount, e.Span())
	return Estimate{
		LowerBound:     target * (1 - margin),
		Target:         target,
		UncappedTarget: target,
		UpperBound:     target * (1 + margin),
	}
}
