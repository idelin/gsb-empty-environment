package fedscheduler

import (
	"errors"
	"math/rand"
	"testing"
)

func regionClusters() []Cluster {
	return []Cluster{
		{Name: "a1", Region: "rA", Zone: "zA1", Weight: 1, Capacity: 10},
		{Name: "a2", Region: "rA", Zone: "zA2", Weight: 1, Capacity: 10},
		{Name: "b1", Region: "rB", Zone: "zB1", Weight: 1, Capacity: 10},
		{Name: "b2", Region: "rB", Zone: "zB2", Weight: 1, Capacity: 10},
		{Name: "c1", Region: "rC", Zone: "zC1", Weight: 1, Capacity: 10},
	}
}

func regionTotals(plan map[string]int64, clusters []Cluster) map[string]int64 {
	out := map[string]int64{}
	for _, c := range clusters {
		out[c.Region] += plan[c.Name]
	}
	return out
}

func zoneTotals(plan map[string]int64, clusters []Cluster) map[string]int64 {
	out := map[string]int64{}
	for _, c := range clusters {
		out[c.Zone] += plan[c.Name]
	}
	return out
}

func TestSpreadMaxReplicas(t *testing.T) {
	clusters := regionClusters()
	constraint := SpreadConstraint{
		Mode:        SpreadReplicasRange,
		MinReplicas: 0,
		MaxReplicas: 5,
	}
	res, err := AllocateWithSpread(12, clusters, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if planSum(res.Replicas) != 12 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
	assertPlanWithinCaps(t, clusters, res.Replicas)
	totals := regionTotals(res.Replicas, clusters)
	for region, v := range totals {
		if v > 5 {
			t.Fatalf("region %s carries %d > maxReplicas 5", region, v)
		}
	}
	// 12 units with per-region cap 5 requires all three regions in use.
	if len(totals) != 3 {
		t.Fatalf("expected 3 used regions, got %d (%v)", len(totals), totals)
	}
}

func TestSpreadMinAndMaxReplicas(t *testing.T) {
	clusters := regionClusters()
	constraint := SpreadConstraint{
		Mode:        SpreadReplicasRange,
		MinReplicas: 2,
		MaxReplicas: 4,
	}
	res, err := AllocateWithSpread(9, clusters, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totals := regionTotals(res.Replicas, clusters)
	for region, v := range totals {
		if v < 2 || v > 4 {
			t.Fatalf("region %s carries %d outside [2,4]", region, v)
		}
	}
	if planSum(res.Replicas) != 9 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
}

func TestSpreadMinGroups(t *testing.T) {
	clusters := regionClusters()
	constraint := SpreadConstraint{Mode: SpreadMinGroups, MinGroups: 3}
	res, err := AllocateWithSpread(4, clusters, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if planSum(res.Replicas) != 4 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
	totals := regionTotals(res.Replicas, clusters)
	used := 0
	for _, v := range totals {
		if v > 0 {
			used++
		}
	}
	if used < 3 {
		t.Fatalf("minGroups violated: only %d regions used (%v)", used, totals)
	}
	assertPlanWithinCaps(t, clusters, res.Replicas)
}

func TestSpreadZoneScope(t *testing.T) {
	clusters := regionClusters()
	constraint := SpreadConstraint{
		Scope:       ScopeZone,
		Mode:        SpreadReplicasRange,
		MaxReplicas: 3,
	}
	res, err := AllocateWithSpread(10, clusters, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totals := zoneTotals(res.Replicas, clusters)
	for zone, v := range totals {
		if v > 3 {
			t.Fatalf("zone %s carries %d > 3", zone, v)
		}
	}
	if planSum(res.Replicas) != 10 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
}

func TestSpreadInfeasible(t *testing.T) {
	clusters := regionClusters()
	cases := []struct {
		name       string
		replicas   int64
		constraint SpreadConstraint
	}{
		{
			name:       "max too tight",
			replicas:   14,
			constraint: SpreadConstraint{Mode: SpreadReplicasRange, MaxReplicas: 4},
		},
		{
			name:       "min too high",
			replicas:   4,
			constraint: SpreadConstraint{Mode: SpreadReplicasRange, MinReplicas: 3, MaxReplicas: 6},
		},
		{
			name:       "minGroups exceeds regions",
			replicas:   6,
			constraint: SpreadConstraint{Mode: SpreadMinGroups, MinGroups: 4},
		},
		{
			name:       "minGroups exceeds replicas",
			replicas:   2,
			constraint: SpreadConstraint{Mode: SpreadMinGroups, MinGroups: 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AllocateWithSpread(tc.replicas, clusters, []SpreadConstraint{tc.constraint}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestSpreadCapacityShortageStillDiagnosed(t *testing.T) {
	clusters := []Cluster{
		{Name: "a1", Region: "rA", Weight: 1, Capacity: 2},
		{Name: "b1", Region: "rB", Weight: 1, Capacity: 2},
	}
	constraint := SpreadConstraint{Mode: SpreadReplicasRange, MaxReplicas: 5}
	_, err := AllocateWithSpread(10, clusters, []SpreadConstraint{constraint})
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("got %v, want ErrInsufficientCapacity", err)
	}
}

func TestSpreadRandomizedInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	for iter := 0; iter < 300; iter++ {
		regionCount := 1 + rng.Intn(4)
		var clusters []Cluster
		var totalCap int64
		for r := 0; r < regionCount; r++ {
			memberCount := 1 + rng.Intn(3)
			for m := 0; m < memberCount; m++ {
				cap := int64(rng.Intn(6))
				clusters = append(clusters, Cluster{
					Name:     "c" + string(rune('a'+r)) + string(rune('a'+m)),
					Region:   "r" + string(rune('a'+r)),
					Weight:   int64(1 + rng.Intn(5)),
					Capacity: cap,
				})
				totalCap += cap
			}
		}

		replicas := int64(rng.Intn(int(totalCap) + 1))
		maxReplicas := int64(1 + rng.Intn(8))
		constraint := SpreadConstraint{Mode: SpreadReplicasRange, MaxReplicas: maxReplicas}
		res, err := AllocateWithSpread(replicas, clusters, []SpreadConstraint{constraint})
		if err != nil {
			// Every error must be a diagnosed infeasibility, never a panic.
			if !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrInsufficientCapacity) {
				t.Fatalf("iter %d: unexpected error %v", iter, err)
			}
			continue
		}
		if planSum(res.Replicas) != replicas {
			t.Fatalf("iter %d: conservation broken %v", iter, res.Replicas)
		}
		assertPlanWithinCaps(t, clusters, res.Replicas)
		totals := regionTotals(res.Replicas, clusters)
		for region, v := range totals {
			if v > maxReplicas {
				t.Fatalf("iter %d: region %s carries %d > %d", iter, region, v, maxReplicas)
			}
		}
	}
}
