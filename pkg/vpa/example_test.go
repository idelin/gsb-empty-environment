package vpa_test

import (
	"fmt"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/eviction"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/recommender"
)

func Example() {
	base := time.Unix(1_700_000_000, 0)

	recommender := recommender.New(recommender.Config{
		Options:          histogram.NewExponentialBucketOptions(0.01, 1.05),
		HalfLife:         24 * time.Hour,
		CPUPercentile:    estimator.Percentile90,
		MemoryPercentile: estimator.Percentile95,
		Mode:             histogram.Linear,
	})
	recommender.SetPolicy(estimator.ContainerResourcePolicy{
		MinAllowed: map[estimator.ResourceName]float64{
			estimator.ResourceMemory: 128 * 1024 * 1024,
		},
	})

	for index := 0; index < 10; index++ {
		recommender.AddCPUSample(0.75, 1, base.Add(time.Duration(index)*time.Minute))
		recommender.AddMemorySample(256*1024*1024, 1, base.Add(time.Duration(index)*time.Minute))
	}

	recommendation := recommender.Recommend(baseTime(base))
	cpu := recommendation[estimator.ResourceCPU]
	fmt.Printf("CPU target=%.3f bounds=[%.3f, %.3f]\n", cpu.Target, cpu.LowerBound, cpu.UpperBound)

	manager := eviction.NewManager(eviction.Config{
		MinReplicas:    1,
		MinChangeRatio: 0.5,
		Cooldown:       10 * time.Minute,
	})
	pod := &eviction.Pod{
		Name: "web-0", Namespace: "default", Running: true,
		Request: eviction.Request{estimator.ResourceCPU: 0.1},
	}
	decision := manager.Evaluate(pod, recommendation, baseTime(base))
	fmt.Printf("eviction eligible=%v reason=%q\n", decision.Eligible, string(decision.Reason))

	// Output:
	// CPU target=0.765 bounds=[0.681, 0.850]
	// eviction eligible=true reason=""
}

func baseTime(base time.Time) time.Time { return base.Add(10 * time.Minute) }
