package histogram

import (
	"math"
	"time"
)

// maxDecayExponent bounds the per-sample weight scaling factor at 2^100.
// 2^100 (~1.27e30) is comfortably inside the float64 range while leaving
// enough headroom that rescaling every existing weight by the same factor can
// never overflow (the largest stored weights stay near the magnitude of the
// newest samples). Whenever a sample would need a larger factor the reference
// time is advanced instead, see shiftReferenceTimestamp.
const maxDecayExponent = 100.0

// DecayingHistogram is an exponentially time-weighted histogram.
//
// A sample (value, weight, t) contributes weight * 2^((t-ref)/halfLife) to the
// bucket owning value, where ref is the current referenceTimestamp. Older
// samples therefore carry exponentially less weight: after one halfLife their
// influence halves. The reference timestamp is transparently advanced (and all
// stored weights rescaled) whenever the raw decay factor would exceed
// 2^maxDecayExponent, which keeps every floating point operation bounded for
// arbitrarily long running processes while leaving quantiles unchanged.
type DecayingHistogram struct {
	options            BucketOptions
	histogram          *Histogram
	halfLife           time.Duration
	referenceTimestamp time.Time
}

// NewDecayingHistogram builds an empty decaying histogram.
func NewDecayingHistogram(options BucketOptions, halfLife time.Duration) *DecayingHistogram {
	if halfLife <= 0 {
		panic("histogram: halfLife must be positive")
	}
	return &DecayingHistogram{
		options:   options,
		histogram: NewHistogram(options),
		halfLife:  halfLife,
	}
}

// HalfLife returns the configured half-life.
func (d *DecayingHistogram) HalfLife() time.Duration { return d.halfLife }

// ReferenceTimestamp returns the anchor time used for weight scaling. It is
// the zero value until the first sample arrives.
func (d *DecayingHistogram) ReferenceTimestamp() time.Time { return d.referenceTimestamp }

// TotalWeight returns the current sum of (decayed) bucket weights.
func (d *DecayingHistogram) TotalWeight() float64 { return d.histogram.TotalWeight() }

// IsEmpty reports whether any sample has been recorded.
func (d *DecayingHistogram) IsEmpty() bool { return d.histogram.IsEmpty() }

// BucketWeight exposes the decayed weight stored in bucket index, mainly for
// diagnostics and tests.
func (d *DecayingHistogram) BucketWeight(index int) float64 {
	return d.histogram.BucketWeight(index)
}

// AddSample folds a new observation into the histogram, advancing the
// reference timestamp first when needed to avoid floating point overflow.
func (d *DecayingHistogram) AddSample(value, weight float64, timestamp time.Time) {
	if weight <= 0 {
		return
	}
	if d.histogram.IsEmpty() {
		d.referenceTimestamp = timestamp
	} else if timestamp.After(d.referenceTimestamp) {
		d.shiftReferenceTimestamp(timestamp)
	}

	factor := d.decayFactor(timestamp)
	d.histogram.AddSample(value, weight*factor)
}

// Percentile returns the requested quantile; see Histogram.Percentile for the
// mode semantics. Rescaling multiplies all weights by the same factor, so the
// result is independent of how often the reference timestamp moved.
func (d *DecayingHistogram) Percentile(p float64, mode PercentileMode) float64 {
	return d.histogram.Percentile(p, mode)
}

// Merge folds every weighted bucket of other into d. other must share the
// bucket layout and half-life; its weights are re-anchored to d's reference
// timestamp before being added.
func (d *DecayingHistogram) Merge(other *DecayingHistogram) {
	if other == nil || other.IsEmpty() {
		return
	}
	if d.halfLife != other.halfLife {
		panic("histogram: cannot merge histograms with different half-lives")
	}
	if d.histogram.IsEmpty() {
		d.referenceTimestamp = other.referenceTimestamp
	} else if other.referenceTimestamp.After(d.referenceTimestamp) {
		d.shiftReferenceTimestamp(other.referenceTimestamp)
	}

	// Convert weights anchored at other.referenceTimestamp into weights
	// anchored at d.referenceTimestamp.
	relativeFactor := math.Exp2(float64(other.referenceTimestamp.Sub(d.referenceTimestamp)) / float64(d.halfLife))
	for index, weight := range other.histogram.buckets {
		if weight != 0 {
			d.histogram.addToBucket(index, weight*relativeFactor)
		}
	}
}

// shiftReferenceTimestamp advances the anchor toward candidate so that the
// decay factor computed for candidate never exceeds 2^maxDecayExponent. Every
// stored weight is divided by the same factor the new samples would otherwise
// be multiplied by, preserving every weight ratio and hence every quantile.
func (d *DecayingHistogram) shiftReferenceTimestamp(candidate time.Time) {
	exponent := float64(candidate.Sub(d.referenceTimestamp)) / float64(d.halfLife)
	if exponent <= maxDecayExponent {
		return
	}
	// Advance by exactly maxDecayExponent half-lives. Repeated calls handle
	// arbitrarily large gaps while keeping every intermediate factor bounded.
	shift := time.Duration(maxDecayExponent * float64(d.halfLife))
	d.referenceTimestamp = d.referenceTimestamp.Add(shift)
	d.histogram.Scale(math.Exp2(-maxDecayExponent))

	// Recurse for the remainder of an unusually large gap.
	d.shiftReferenceTimestamp(candidate)
}

// decayFactor computes 2^((timestamp-referenceTimestamp)/halfLife).
func (d *DecayingHistogram) decayFactor(timestamp time.Time) float64 {
	exponent := float64(timestamp.Sub(d.referenceTimestamp)) / float64(d.halfLife)
	return math.Exp2(exponent)
}
