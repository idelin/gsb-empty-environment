package recommender

import (
	"testing"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/oom"
)

var baseTime = time.Unix(1_700_000_000, 0)

func newTestRecommender() *ContainerRecommender {
	return New(Config{
		Options:          histogram.NewExponentialBucketOptions(0.01, 1.05),
		HalfLife:         24 * time.Hour,
		CPUPercentile:    estimator.Percentile90,
		MemoryPercentile: estimator.Percentile90,
		Mode:             histogram.Linear,
	})
}

func TestOOMBumpDominatesLowPercentile(t *testing.T) {
	r := newTestRecommender()

	// Many low, stable memory samples keep the ordinary P90 low.
	for index := 0; index < 20; index++ {
		r.AddMemorySample(100*1024*1024, 1, baseTime.Add(time.Duration(index)*time.Minute))
	}

	oomLimit := int64(500 * 1024 * 1024)
	bumped := r.RecordOOM(oomLimit, baseTime.Add(25*time.Minute))
	wantBumped := oomLimit + oom.DefaultMinBumpUp
	if bumped != wantBumped {
		t.Fatalf("bumped sample = %d, want %d", bumped, wantBumped)
	}

	recommendation := r.Recommend(baseTime.Add(30 * time.Minute))
	memory, ok := recommendation[estimator.ResourceMemory]
	if !ok {
		t.Fatal("memory recommendation missing")
	}
	if memory.LowerBound < float64(wantBumped) {
		t.Fatalf("lower bound %v undercut OOM floor %d", memory.LowerBound, wantBumped)
	}
	if memory.Target < memory.LowerBound {
		t.Fatalf("target %v below lower bound %v", memory.Target, memory.LowerBound)
	}

	// After the OOM window expires the floor disappears.
	expired := r.Recommend(baseTime.Add(30*time.Minute + oom.DefaultWindow + time.Minute))
	if expired[estimator.ResourceMemory].LowerBound >= float64(wantBumped) {
		t.Fatal("OOM floor should expire with the sliding window")
	}
}

func TestRecommendationClampedByPolicyWithUncappedKept(t *testing.T) {
	r := newTestRecommender()
	r.SetPolicy(estimator.ContainerResourcePolicy{
		MinAllowed: map[estimator.ResourceName]float64{
			estimator.ResourceCPU:    0.5,
			estimator.ResourceMemory: 1 << 30,
		},
		MaxAllowed: map[estimator.ResourceName]float64{
			estimator.ResourceCPU: 2.0,
		},
	})

	for index := 0; index < 30; index++ {
		r.AddCPUSample(0.1, 1, baseTime.Add(time.Duration(index)*time.Minute))
		r.AddMemorySample(50*1024*1024, 1, baseTime.Add(time.Duration(index)*time.Minute))
	}

	recommendation := r.Recommend(baseTime.Add(31 * time.Minute))
	cpu := recommendation[estimator.ResourceCPU]
	if cpu.Target != 0.5 || cpu.LowerBound != 0.5 {
		t.Fatalf("CPU not clamped to minAllowed: %+v", cpu)
	}
	if cpu.UncappedTarget == 0.5 {
		t.Fatalf("uncapped CPU target must preserve raw estimate, got %v", cpu.UncappedTarget)
	}
	memory := recommendation[estimator.ResourceMemory]
	if memory.Target < 1<<30 {
		t.Fatalf("memory target %v below minAllowed", memory.Target)
	}
}

func TestControlledResourcesExcludesMemory(t *testing.T) {
	r := newTestRecommender()
	r.SetPolicy(estimator.ContainerResourcePolicy{
		ControlledResources: []estimator.ResourceName{estimator.ResourceCPU},
	})
	r.AddCPUSample(1, 1, baseTime)
	r.AddMemorySample(1<<30, 1, baseTime)

	recommendation := r.Recommend(baseTime)
	if _, ok := recommendation[estimator.ResourceMemory]; ok {
		t.Fatal("memory must be absent when not controlled")
	}
	if _, ok := recommendation[estimator.ResourceCPU]; !ok {
		t.Fatal("CPU recommendation missing")
	}
}

func TestFullPipelineQuantiles(t *testing.T) {
	r := newTestRecommender()
	for index := 0; index < 50; index++ {
		r.AddCPUSample(1.0+float64(index%5)*0.1, 1, baseTime.Add(time.Duration(index)*time.Minute))
		r.AddMemorySample(float64((256+index%4*64))*1024*1024, 1, baseTime.Add(time.Duration(index)*time.Minute))
	}

	recommendation := r.Recommend(baseTime.Add(time.Hour))
	cpu := recommendation[estimator.ResourceCPU]
	if !(cpu.LowerBound > 0 && cpu.LowerBound <= cpu.Target && cpu.Target <= cpu.UpperBound) {
		t.Fatalf("inconsistent CPU estimate: %+v", cpu)
	}
	memory := recommendation[estimator.ResourceMemory]
	if memory.Target != float64(int64(memory.Target)) {
		t.Fatalf("memory target must be integer bytes, got %v", memory.Target)
	}
}
