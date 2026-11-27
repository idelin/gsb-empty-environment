package vpa_test

import (
	"math"
	"testing"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/eviction"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/oom"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/recommender"
)

// TestEndToEndVPAWorkflow exercises the full acceptance chain:
// decaying-histogram quantiles, OOM floor, policy clamping and gated
// eviction in a single scenario.
func TestEndToEndVPAWorkflow(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	rec := recommender.New(recommender.Config{
		Options:          histogram.NewExponentialBucketOptions(0.01, 1.05),
		HalfLife:         24 * time.Hour,
		CPUPercentile:    estimator.Percentile90,
		MemoryPercentile: estimator.Percentile90,
		Mode:             histogram.Linear,
	})
	rec.SetPolicy(estimator.ContainerResourcePolicy{
		MinAllowed: map[estimator.ResourceName]float64{
			estimator.ResourceCPU:    0.1,
			estimator.ResourceMemory: 64 * 1024 * 1024,
		},
		MaxAllowed: map[estimator.ResourceName]float64{
			estimator.ResourceCPU: 4.0,
		},
	})

	// A week of stable, modest usage.
	for day := 0; day < 7; day++ {
		for sample := 0; sample < 12; sample++ {
			at := base.Add(time.Duration(day)*24*time.Hour + time.Duration(sample)*time.Hour)
			rec.AddCPUSample(0.5, 1, at)
			rec.AddMemorySample(200*1024*1024, 1, at)
		}
	}

	// Then the container OOMs at a 300Mi limit.
	oomAt := base.Add(7 * 24 * time.Hour)
	bumped := rec.RecordOOM(300*1024*1024, oomAt)

	recommendation := rec.Recommend(oomAt)
	memory := recommendation[estimator.ResourceMemory]
	if memory.LowerBound < float64(bumped) {
		t.Fatalf("memory lower bound %v below OOM bump %d", memory.LowerBound, bumped)
	}
	if memory.UncappedTarget == 0 || memory.Target < memory.LowerBound {
		t.Fatalf("inconsistent memory estimate: %+v", memory)
	}
	cpu := recommendation[estimator.ResourceCPU]
	if cpu.Target < 0.1 || cpu.Target > 4.0 {
		t.Fatalf("CPU recommendation outside policy: %+v", cpu)
	}

	// Two pods request far less than the recommendation; only one may be
	// evicted per round, bounded by the PDB and the minReplicas floor.
	manager := eviction.NewManager(eviction.Config{
		MinReplicas:    2,
		MinChangeRatio: 0.5,
		Cooldown:       10 * time.Minute,
	})
	manager.UpsertPDB(&eviction.PodDisruptionBudget{
		Name:               "web-pdb",
		Namespace:          "default",
		SelectorPods:       map[string]bool{"web-0": true, "web-1": true, "web-2": true},
		DisruptionsAllowed: 1,
	})
	pods := []*eviction.Pod{
		{
			Name: "web-0", Namespace: "default", Running: true,
			Request: eviction.Request{
				estimator.ResourceCPU:    0.05,
				estimator.ResourceMemory: 100 * 1024 * 1024,
			},
		},
		{
			Name: "web-1", Namespace: "default", Running: true,
			Request: eviction.Request{
				estimator.ResourceCPU:    0.08,
				estimator.ResourceMemory: 150 * 1024 * 1024,
			},
		},
		{
			Name: "web-2", Namespace: "default", Running: true,
			Request: eviction.Request{
				estimator.ResourceCPU:    0.06,
				estimator.ResourceMemory: 120 * 1024 * 1024,
			},
		},
	}

	evicted, rejected := manager.EvictRound(pods, recommendation, oomAt)
	if evicted == nil {
		t.Fatalf("expected one eviction, rejected=%+v", rejected)
	}
	if evicted.Pod.Name != "web-0" {
		t.Fatalf("expected most deviant web-0, got %s", evicted.Pod.Name)
	}

	// The PDB budget is now exhausted: the next round evicts nobody.
	evicted, _ = manager.EvictRound(pods, recommendation, oomAt.Add(11*time.Minute))
	if evicted != nil {
		t.Fatal("second round must be blocked by the exhausted PDB")
	}

	// Cooldown still blocks the freshly evicted pod even after budget refill.
	manager.UpsertPDB(&eviction.PodDisruptionBudget{
		Name: "web-pdb", Namespace: "default",
		SelectorPods:       map[string]bool{"web-0": true, "web-1": true, "web-2": true},
		DisruptionsAllowed: 1,
	})
	decision := manager.Evaluate(pods[0], recommendation, oomAt.Add(5*time.Minute))
	if decision.Reason != eviction.ReasonCooldown {
		t.Fatalf("expected cooldown rejection, got %s", decision.Reason)
	}
}

func TestHistogramOOMEstimatorPrimitives(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	halfLife := time.Hour
	h := histogram.NewDecayingHistogram(
		histogram.NewExponentialBucketOptions(1.0, 2.0), halfLife)

	// Identical samples spread over 300 half-lives exercise rescaling while
	// keeping every relative weight ratio exactly representable.
	h.AddSample(2, 1, base)
	h.AddSample(8, 1, base.Add(300*halfLife))
	if math.IsInf(h.TotalWeight(), 0) || math.IsNaN(h.TotalWeight()) {
		t.Fatalf("overflow across 300 half-lives: %v", h.TotalWeight())
	}
	if got := h.Percentile(1, histogram.UpperBound); got != 16 {
		t.Fatalf("P100 upper bound = %v, want 16", got)
	}

	bumper := oom.NewBumper(oom.DefaultBumpUpRatio, oom.DefaultMinBumpUp, time.Hour)
	bumped := bumper.Record(oom.Event{Memory: 1 << 30, At: base})
	wantBumped := bumper.BumpMemory(1 << 30)
	if bumped != wantBumped {
		t.Fatalf("bumped value %d, want %d", bumped, wantBumped)
	}
	if floor, ok := bumper.BumpFloor(base.Add(30 * time.Minute)); !ok || floor != bumped {
		t.Fatalf("active OOM floor wrong: %d ok=%v", floor, ok)
	}
	if _, ok := bumper.BumpFloor(base.Add(2 * time.Hour)); ok {
		t.Fatal("OOM floor must expire with the window")
	}

	est := estimator.NewEstimator(
		histogram.NewDecayingHistogram(
			histogram.NewExponentialBucketOptions(0.01, 1.05), halfLife),
		estimator.Percentile95, histogram.Linear)
	est.AddSample(10, 1, base)
	oneSample := est.Estimate()
	for index := 0; index < 100; index++ {
		est.AddSample(10, 1, base.Add(time.Duration(index)*time.Minute))
	}
	manySamples := est.Estimate()
	if manySamples.UpperBound-manySamples.LowerBound >=
		oneSample.UpperBound-oneSample.LowerBound {
		t.Fatal("confidence interval must narrow as samples accumulate")
	}
}
