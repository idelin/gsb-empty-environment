package estimator

import (
	"math"
	"testing"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
)

func newTestEstimator(percentile float64) *Estimator {
	options := histogram.NewExponentialBucketOptions(1.0, 2.0)
	h := histogram.NewDecayingHistogram(options, 24*time.Hour)
	return NewEstimator(h, percentile, histogram.Linear)
}

func TestFewSamplesWidenConfidenceInterval(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	few := newTestEstimator(Percentile90)
	few.AddSample(10, 1, base)
	fewEstimate := few.Estimate()

	many := newTestEstimator(Percentile90)
	for index := 0; index < 200; index++ {
		many.AddSample(10, 1, base.Add(time.Duration(index)*time.Hour))
	}
	manyEstimate := many.Estimate()

	fewWidth := fewEstimate.UpperBound - fewEstimate.LowerBound
	manyWidth := manyEstimate.UpperBound - manyEstimate.LowerBound
	if fewWidth <= manyWidth {
		t.Fatalf("one-sample width %v should exceed many-sample width %v", fewWidth, manyWidth)
	}

	// Margin shrinks monotonically as samples accumulate.
	m1 := confidenceMargin(1, 0)
	m10 := confidenceMargin(10, 10*time.Hour)
	m100 := confidenceMargin(100, 100*time.Hour)
	if !(m1 > m10 && m10 > m100) {
		t.Fatalf("margins not monotonic: %v %v %v", m1, m10, m100)
	}
	if m1 > maxMargin || m100 < minMargin {
		t.Fatalf("margins outside configured bounds: %v %v", m1, m100)
	}

	// Bounds stay symmetric around the target.
	if math.Abs((fewEstimate.UpperBound-fewEstimate.Target)-(fewEstimate.Target-fewEstimate.LowerBound)) > 1e-9 {
		t.Fatal("confidence interval not symmetric around target")
	}
}

func TestSpanAlsoNarrowsInterval(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	short := newTestEstimator(Percentile90)
	long := newTestEstimator(Percentile90)
	for index := 0; index < 20; index++ {
		short.AddSample(10, 1, base.Add(time.Duration(index)*time.Minute))
		long.AddSample(10, 1, base.Add(time.Duration(index)*6*time.Hour))
	}
	if short.Estimate().UpperBound-short.Estimate().LowerBound <=
		long.Estimate().UpperBound-long.Estimate().LowerBound {
		t.Fatal("longer sample span should narrow the interval at equal count")
	}
}

func TestApplyPolicyClampsBoundsAndKeepsUncapped(t *testing.T) {
	estimate := Estimate{
		LowerBound:     10,
		Target:         20,
		UncappedTarget: 20,
		UpperBound:     30,
	}
	policy := ContainerResourcePolicy{
		MinAllowed: map[ResourceName]float64{ResourceMemory: 15},
		MaxAllowed: map[ResourceName]float64{ResourceMemory: 25},
	}

	clamped := ApplyPolicy(estimate, ResourceMemory, policy)
	if clamped.LowerBound != 15 || clamped.Target != 20 || clamped.UpperBound != 25 {
		t.Fatalf("unexpected clamped estimate: %+v", clamped)
	}
	if clamped.UncappedTarget != 20 {
		t.Fatalf("uncapped target mutated: %v", clamped.UncappedTarget)
	}
}

func TestApplyPolicyExtremeClampsPreserveUncapped(t *testing.T) {
	estimate := Estimate{
		LowerBound:     10,
		Target:         20,
		UncappedTarget: 20,
		UpperBound:     30,
	}
	policy := ContainerResourcePolicy{
		MinAllowed: map[ResourceName]float64{ResourceMemory: 40},
		MaxAllowed: map[ResourceName]float64{ResourceMemory: 50},
	}

	clamped := ApplyPolicy(estimate, ResourceMemory, policy)
	if clamped.LowerBound != 40 || clamped.Target != 40 || clamped.UpperBound != 40 {
		t.Fatalf("expected all bounds on minAllowed, got %+v", clamped)
	}
	if clamped.UncappedTarget != 20 {
		t.Fatalf("uncapped target lost: %v", clamped.UncappedTarget)
	}

	maxOnly := ContainerResourcePolicy{
		MaxAllowed: map[ResourceName]float64{ResourceMemory: 5},
	}
	clamped = ApplyPolicy(estimate, ResourceMemory, maxOnly)
	if clamped.LowerBound != 5 || clamped.Target != 5 || clamped.UpperBound != 5 {
		t.Fatalf("expected all bounds on maxAllowed, got %+v", clamped)
	}
	if clamped.UncappedTarget != 20 {
		t.Fatalf("uncapped target lost: %v", clamped.UncappedTarget)
	}
}

func TestControlledResourcesFiltering(t *testing.T) {
	policy := ContainerResourcePolicy{
		ControlledResources: []ResourceName{ResourceCPU},
		MinAllowed:          map[ResourceName]float64{ResourceMemory: 999},
	}
	estimate := Estimate{LowerBound: 1, Target: 2, UncappedTarget: 2, UpperBound: 3}

	if policy.IsControlled(ResourceMemory) {
		t.Fatal("memory must not be controlled")
	}
	got := ApplyPolicy(estimate, ResourceMemory, policy)
	if got != estimate {
		t.Fatalf("uncontrolled resource estimate changed: %+v", got)
	}
}

func TestEmptyEstimatorReturnsZero(t *testing.T) {
	if got := newTestEstimator(Percentile90).Estimate(); got != (Estimate{}) {
		t.Fatalf("empty estimator should return zero estimate, got %+v", got)
	}
}
