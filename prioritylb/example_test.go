package prioritylb_test

import (
	"fmt"
	"time"

	"prescreen.local/ps-cdd9f54284/prioritylb"
)

func ExampleCalculate() {
	now := time.Unix(1_700_000_000, 0)

	groups := []prioritylb.PriorityGroup{
		// P0: 7 of 10 endpoints healthy -> capped availability 0.7*1.4 = 0.98.
		{Endpoints: buildGroup(7, 3, now)},
		// P1: fully healthy and absorbs the 2% overflow from P0.
		{Endpoints: buildGroup(10, 0, now)},
	}

	cfg := prioritylb.Config{
		Now:                    now,
		OverprovisioningFactor: 1.4,
		PanicThreshold:         0.5,
		SlowStartWindow:        30 * time.Second,
		AggressionExponent:     1,
		MinWeightFloor:         0.1,
	}

	result, err := prioritylb.Calculate(groups, cfg)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("panic=%v\n", result.Panic)
	for _, share := range result.PriorityShares {
		fmt.Printf("P%d share=%.2f\n", share.Priority, share.Share)
	}

	// Probabilities sum to exactly 1.0.
	total := 0.0
	for _, ep := range result.Endpoints {
		total += ep.Probability
	}
	fmt.Printf("probability sum=%.1f\n", total)
	// Output:
	// panic=false
	// P0 share=0.98
	// P1 share=0.02
	// probability sum=1.0
}

func buildGroup(healthy, unhealthy int, now time.Time) []prioritylb.Endpoint {
	endpoints := make([]prioritylb.Endpoint, 0, healthy+unhealthy)
	for i := 0; i < healthy; i++ {
		endpoints = append(endpoints, prioritylb.Endpoint{
			ID:       fmt.Sprintf("h%d", i),
			Weight:   1,
			State:    prioritylb.Healthy,
			OnlineAt: now.Add(-time.Minute),
		})
	}
	for i := 0; i < unhealthy; i++ {
		endpoints = append(endpoints, prioritylb.Endpoint{
			ID:       fmt.Sprintf("u%d", i),
			Weight:   1,
			State:    prioritylb.Unhealthy,
			OnlineAt: now.Add(-time.Minute),
		})
	}
	return endpoints
}
