package histogram

import (
	"math"
	"testing"
	"time"
)

func testOptions() ExponentialBucketOptions {
	return NewExponentialBucketOptions(1.0, 2.0)
}

func TestBasicPercentileModes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := NewDecayingHistogram(testOptions(), time.Hour)

	// Two equal-weight samples at the same timestamp: no decay difference.
	h.AddSample(1.0, 1, now)
	h.AddSample(3.0, 1, now)

	if got := h.Percentile(0, Linear); got != 0 {
		t.Fatalf("P0 = %v, want 0", got)
	}
	// P25 sits in the middle of the first occupied bucket [1, 2).
	got := h.Percentile(0.25, Linear)
	if math.Abs(got-1.5) > 1e-12 {
		t.Fatalf("linear P25 = %v, want 1.5", got)
	}
	upper := h.Percentile(0.25, UpperBound)
	if upper != 2.0 {
		t.Fatalf("upper-bound P25 = %v, want bucket upper edge 2.0", upper)
	}
	// At the exact bucket boundary interpolation reaches the upper edge.
	if got := h.Percentile(0.5, Linear); got != 2.0 {
		t.Fatalf("linear P50 = %v, want boundary 2.0", got)
	}
	if got := h.Percentile(1, Linear); got != 4.0 {
		t.Fatalf("P100 = %v, want highest bucket upper edge 4.0", got)
	}
}

func TestHalfLifeDecayWeights(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := NewDecayingHistogram(testOptions(), halfLife)

	h.AddSample(1.0, 1, base)
	h.AddSample(1.0, 1, base.Add(halfLife))

	// Newer samples gain factor 2^(age/halfLife): the new sample weighs 2
	// while the anchor sample keeps weight 1. Their ratio is still 1:2, i.e.
	// the old observation lost half its relative influence per half-life.
	if got := h.TotalWeight(); math.Abs(got-3.0) > 1e-12 {
		t.Fatalf("total weight = %v, want 3", got)
	}

	// A sample two half-lives later weighs 4x; the oldest relative influence
	// is down to 25%.
	h2 := NewDecayingHistogram(testOptions(), halfLife)
	h2.AddSample(1.0, 1, base)
	h2.AddSample(1.0, 1, base.Add(2*halfLife))
	if got := h2.TotalWeight(); math.Abs(got-5.0) > 1e-12 {
		t.Fatalf("two half-life total weight = %v, want 5", got)
	}
}

func TestRescaleIsQuantileIdentity(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	options := testOptions()

	values := []float64{0.5, 1.2, 3.4, 7.8, 15.1, 31.9, 63.2}
	h := NewDecayingHistogram(options, halfLife)

	for index, value := range values {
		h.AddSample(value, 1, base.Add(time.Duration(index)*time.Minute))
	}

	quantiles := map[float64]map[PercentileMode]float64{}
	for _, p := range []float64{0.5, 0.9, 0.95, 1.0} {
		quantiles[p] = map[PercentileMode]float64{
			Linear:     h.Percentile(p, Linear),
			UpperBound: h.Percentile(p, UpperBound),
		}
	}
	totalBefore := h.TotalWeight()

	// Force three 2^100 rescalings. Rescaling multiplies every weight by the
	// same factor, which is an identity transformation on every quantile.
	h.shiftReferenceTimestamp(h.ReferenceTimestamp().Add(350 * halfLife))

	for index, weight := range h.histogram.buckets {
		if math.IsInf(weight, 0) || math.IsNaN(weight) {
			t.Fatalf("bucket %d weight non-finite: %v", index, weight)
		}
	}
	if math.IsInf(h.TotalWeight(), 0) || math.IsNaN(h.TotalWeight()) {
		t.Fatalf("total weight non-finite after rescale: %v", h.TotalWeight())
	}
	if math.Abs(h.TotalWeight()-totalBefore*math.Exp2(-3*maxDecayExponent)) > 1e-20 {
		t.Fatalf("total = %v, want uniform 2^-300 scaling", h.TotalWeight())
	}

	for _, p := range []float64{0.5, 0.9, 0.95, 1.0} {
		for _, mode := range []PercentileMode{Linear, UpperBound} {
			want := quantiles[p][mode]
			got := h.Percentile(p, mode)
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("p=%v mode=%v: post-rescale %v != pre-rescale %v", p, mode, got, want)
			}
		}
	}

	// New samples after the anchor jump remain bounded and retrievable.
	h.AddSample(100.0, 1, h.ReferenceTimestamp().Add(time.Minute))
	if math.IsInf(h.TotalWeight(), 0) {
		t.Fatal("total weight overflowed after post-rescale sample")
	}
}

func TestWeightRatiosSurviveRescale(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := NewDecayingHistogram(testOptions(), halfLife)

	// Two samples 250 half-lives apart in distinct buckets. Rescaling must not
	// change their theoretical 2^-250 relative weight ratio.
	h.AddSample(1.0, 1, base)
	h.AddSample(50.0, 1, base.Add(250*halfLife))

	oldWeight := h.BucketWeight(bucketIndex(1.0, testOptions()))
	newWeight := h.BucketWeight(bucketIndex(50.0, testOptions()))
	ratio := oldWeight / newWeight
	want := math.Exp2(-250)
	if math.Abs(ratio-want) > want*1e-9 {
		t.Fatalf("weight ratio = %v, want %v", ratio, want)
	}
	if math.IsInf(newWeight, 0) || math.IsInf(oldWeight, 0) {
		t.Fatal("bucket weights overflowed")
	}
}

func TestRescaleAdvancesReferenceTimestamp(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := NewDecayingHistogram(testOptions(), halfLife)
	h.AddSample(1.0, 1, base)

	h.AddSample(2.0, 1, base.Add(150*halfLife))

	// The anchor must have moved by one full 100-half-life shift.
	wantRef := base.Add(time.Duration(maxDecayExponent) * halfLife)
	if !h.ReferenceTimestamp().Equal(wantRef) {
		t.Fatalf("reference = %v, want %v", h.ReferenceTimestamp(), wantRef)
	}
	// The oldest weight was rescaled twice: 2^-100 on the shift, then its
	// decay to the new anchor is 2^-50.
	oldWeight := h.BucketWeight(bucketIndex(1.0, testOptions()))
	wantOld := math.Exp2(-150)
	if math.Abs(oldWeight-wantOld) > 1e-20 {
		t.Fatalf("old bucket weight = %v, want %v", oldWeight, wantOld)
	}
}

func TestHugeTimeSpanNoOverflow(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := NewDecayingHistogram(testOptions(), halfLife)
	h.AddSample(1.0, 1, base)

	// 5000 half-lives: five rescalings plus an enormous final decay.
	h.AddSample(2.0, 1, base.Add(5000*halfLife))

	if math.IsInf(h.TotalWeight(), 0) || math.IsNaN(h.TotalWeight()) {
		t.Fatalf("total weight non-finite after 5000 half-lives: %v", h.TotalWeight())
	}
	// The newest sample always keeps unit weight, so the P100 upper edge of
	// the newest value bucket must remain retrievable.
	if got := h.Percentile(1, UpperBound); got != bucketUpperBound(bucketIndex(2.0, testOptions()), testOptions()) {
		t.Fatalf("P100 = %v after huge span", got)
	}
}

func TestMergeReanchorsWeights(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	options := testOptions()

	merged := NewDecayingHistogram(options, halfLife)
	other := NewDecayingHistogram(options, halfLife)
	merged.AddSample(1.0, 1, base)
	other.AddSample(3.0, 1, base.Add(halfLife))

	merged.Merge(other)

	// Reference remains the earliest anchor; other's weight re-anchors with
	// factor 2^1, giving total 1 + 2.
	if got := merged.TotalWeight(); math.Abs(got-3.0) > 1e-12 {
		t.Fatalf("merged total = %v, want 3", got)
	}
}

func TestNegativeTimestampsWeakenOldSamples(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := NewDecayingHistogram(testOptions(), halfLife)

	h.AddSample(1.0, 1, base.Add(halfLife))
	h.AddSample(1.0, 1, base)

	// First sample is older relative to the anchor, so it must decay to 0.5.
	want := 1.5
	if got := h.TotalWeight(); math.Abs(got-want) > 1e-12 {
		t.Fatalf("total = %v, want %v", got, want)
	}
}
