package fedscheduler

import (
	"errors"
	"math/rand"
	"testing"
)

func TestRebalanceKeepsFrozenReplicasOnWeightDrift(t *testing.T) {
	// Existing plan is feasible under the new caps; weights change but R does
	// not. KeepExisting must not evict a single running replica.
	clusters := []Cluster{
		{Name: "c1", Weight: 9, Capacity: 100},
		{Name: "c2", Weight: 1, Capacity: 100},
	}
	existing := map[string]int64{"c1": 5, "c2": 5}
	res, err := Rebalance(10, clusters, existing, KeepExisting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated != 0 {
		t.Fatalf("weight drift alone must not cause evictions, migrated=%d", res.Migrated)
	}
	if !mapsEqual(res.Replicas, existing) {
		t.Fatalf("frozen plan changed: %v", res.Replicas)
	}
}

func TestRebalanceShrinksOverCapCluster(t *testing.T) {
	// c1's capacity shrank from 10 to 3: exactly 4 replicas must be shed,
	// then re-placed onto c2 by weight.
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 3},
		{Name: "c2", Weight: 1, Capacity: 100},
	}
	existing := map[string]int64{"c1": 7, "c2": 3}
	res, err := Rebalance(10, clusters, existing, KeepExisting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int64{"c1": 3, "c2": 7}
	if !mapsEqual(res.Replicas, want) {
		t.Fatalf("got %v, want %v", res.Replicas, want)
	}
	if res.Migrated != 4 || res.MinEvictions != 4 {
		t.Fatalf("migrated=%d min=%d, want 4/4", res.Migrated, res.MinEvictions)
	}
}

func TestRebalanceScaleUpFillsByWeight(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 100},
		{Name: "c2", Weight: 3, Capacity: 100},
	}
	existing := map[string]int64{"c1": 2, "c2": 2}
	res, err := Rebalance(10, clusters, existing, KeepExisting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated != 0 {
		t.Fatalf("scale-up must not evict, migrated=%d", res.Migrated)
	}
	// The frozen bases (2,2) are kept and the delta of 8 is split by
	// relative weights 1:3, i.e. 2 and 6, yielding (4,6).
	want := map[string]int64{"c1": 4, "c2": 6}
	if !mapsEqual(res.Replicas, want) {
		t.Fatalf("got %v, want %v", res.Replicas, want)
	}
	if planSum(res.Replicas) != 10 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
}

func TestRebalanceScaleDownMinEvictions(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 100},
		{Name: "c2", Weight: 1, Capacity: 100},
		{Name: "c3", Weight: 1, Capacity: 100},
	}
	existing := map[string]int64{"c1": 4, "c2": 3, "c3": 3}
	res, err := Rebalance(6, clusters, existing, KeepExisting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Migrated != 4 {
		t.Fatalf("downscale by 4 must migrate exactly 4, got %d", res.Migrated)
	}
	if planSum(res.Replicas) != 6 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
	assertPlanWithinCaps(t, clusters, res.Replicas)
}

func TestRebalanceInsufficientCapacity(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 2},
		{Name: "c2", Weight: 1, Capacity: 3},
	}
	existing := map[string]int64{"c1": 2, "c2": 3}
	_, err := Rebalance(10, clusters, existing, KeepExisting)
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("got %v, want ErrInsufficientCapacity", err)
	}
	var ice *InsufficientCapacityError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *InsufficientCapacityError, got %T", err)
	}
	if ice.Deficit != 5 || !mapsEqual(ice.MaxPlan, map[string]int64{"c1": 2, "c2": 3}) {
		t.Fatalf("unexpected diagnostic: %+v", ice)
	}
}

func TestRebalanceRegionMaxForcesEviction(t *testing.T) {
	clusters := []Cluster{
		{Name: "a1", Region: "rA", Weight: 1, Capacity: 10},
		{Name: "b1", Region: "rB", Weight: 1, Capacity: 10},
	}
	existing := map[string]int64{"a1": 8, "b1": 2}
	constraint := SpreadConstraint{Mode: SpreadReplicasRange, MaxReplicas: 5}
	res, err := RebalanceWithSpread(10, clusters, existing, KeepExisting, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totals := regionTotals(res.Replicas, clusters)
	if totals["rA"] != 5 || totals["rB"] != 5 {
		t.Fatalf("region totals %v, want rA=5 rB=5", totals)
	}
	if res.Migrated != 3 {
		t.Fatalf("exactly 3 replicas must leave rA, migrated=%d", res.Migrated)
	}
}

func TestRebalanceMinGroupsSeedsEmptyRegion(t *testing.T) {
	clusters := []Cluster{
		{Name: "a1", Region: "rA", Weight: 1, Capacity: 10},
		{Name: "b1", Region: "rB", Weight: 1, Capacity: 10},
		{Name: "c1", Region: "rC", Weight: 1, Capacity: 10},
	}
	existing := map[string]int64{"a1": 6, "b1": 0, "c1": 0}
	constraint := SpreadConstraint{Mode: SpreadMinGroups, MinGroups: 3}
	res, err := RebalanceWithSpread(6, clusters, existing, KeepExisting, []SpreadConstraint{constraint})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totals := regionTotals(res.Replicas, clusters)
	used := 0
	for _, v := range totals {
		if v > 0 {
			used++
		}
	}
	if used < 3 {
		t.Fatalf("minGroups violated: %v", totals)
	}
	// Two empty regions must each receive one replica: two migrations.
	if res.Migrated != 2 {
		t.Fatalf("expected 2 migrations, got %d (%v)", res.Migrated, res.Replicas)
	}
}

// TestRebalanceMinimalEvictionsBruteForce exhaustively enumerates every
// feasible capped plan on tiny instances and proves the KeepExisting result
// attains the global minimum migration count sum_i max(0, existing_i - new_i).
func TestRebalanceMinimalEvictionsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(3)
		clusters := make([]Cluster, n)
		existing := map[string]int64{}
		var totalCap int64
		for i := 0; i < n; i++ {
			name := "c" + string(rune('a'+i))
			capacity := int64(rng.Intn(5))
			clusters[i] = Cluster{Name: name, Weight: int64(1 + rng.Intn(4)), Capacity: capacity}
			totalCap += capacity
			if rng.Intn(2) == 0 {
				existing[name] = int64(rng.Intn(6))
			}
		}
		replicas := int64(rng.Intn(int(totalCap) + 1))

		res, err := Rebalance(replicas, clusters, existing, KeepExisting)
		if err != nil {
			if errors.Is(err, ErrInsufficientCapacity) {
				continue
			}
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}

		minimum := bruteForceMinEvictions(clusters, existing, replicas)
		if res.Migrated != minimum {
			t.Fatalf("iter %d: migrated %d but brute-force minimum is %d (plan %v, existing %v)",
				iter, res.Migrated, minimum, res.Replicas, existing)
		}
		if res.MinEvictions != minimum {
			t.Fatalf("iter %d: MinEvictions %d != brute-force %d", iter, res.MinEvictions, minimum)
		}
		if planSum(res.Replicas) != replicas {
			t.Fatalf("iter %d: conservation broken", iter)
		}
		assertPlanWithinCaps(t, clusters, res.Replicas)
	}
}

// bruteForceMinEvictions enumerates all capped allocations summing to
// replicas and returns the smallest achievable eviction count.
func bruteForceMinEvictions(clusters []Cluster, existing map[string]int64, replicas int64) int64 {
	best := int64(1 << 62)
	var enumerate func(idx int, remaining int64, plan map[string]int64)
	enumerate = func(idx int, remaining int64, plan map[string]int64) {
		if idx == len(clusters) {
			if remaining != 0 {
				return
			}
			var evicted int64
			for _, c := range clusters {
				if existing[c.Name] > plan[c.Name] {
					evicted += existing[c.Name] - plan[c.Name]
				}
			}
			if evicted < best {
				best = evicted
			}
			return
		}
		upper := clusters[idx].Capacity
		if upper > remaining {
			upper = remaining
		}
		for v := int64(0); v <= upper; v++ {
			plan[clusters[idx].Name] = v
			enumerate(idx+1, remaining-v, plan)
		}
	}
	enumerate(0, replicas, map[string]int64{})
	return best
}

// TestRebalanceConstrainedMinimalEvictionsBruteForce repeats the proof under
// Region maxReplicas constraints.
func TestRebalanceConstrainedMinimalEvictionsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	for iter := 0; iter < 300; iter++ {
		regionCount := 1 + rng.Intn(3)
		var clusters []Cluster
		existing := map[string]int64{}
		var totalCap int64
		for r := 0; r < regionCount; r++ {
			capacity := int64(rng.Intn(4))
			name := "c" + string(rune('a'+r))
			clusters = append(clusters, Cluster{
				Name:     name,
				Region:   "r" + string(rune('a'+r)),
				Weight:   int64(1 + rng.Intn(3)),
				Capacity: capacity,
			})
			totalCap += capacity
			if rng.Intn(2) == 0 {
				existing[name] = int64(rng.Intn(5))
			}
		}
		replicas := int64(rng.Intn(int(totalCap) + 1))
		maxReplicas := int64(1 + rng.Intn(5))
		constraint := SpreadConstraint{Mode: SpreadReplicasRange, MaxReplicas: maxReplicas}

		res, err := RebalanceWithSpread(replicas, clusters, existing, KeepExisting, []SpreadConstraint{constraint})
		if err != nil {
			if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrInsufficientCapacity) {
				continue
			}
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}

		minimum, feasible := bruteForceMinEvictionsWithRegionMax(clusters, existing, replicas, maxReplicas)
		if !feasible {
			t.Fatalf("iter %d: engine returned a plan but brute force found none feasible", iter)
		}
		if res.Migrated != minimum {
			t.Fatalf("iter %d: migrated %d but brute-force minimum is %d (plan %v, existing %v)",
				iter, res.Migrated, minimum, res.Replicas, existing)
		}
		totals := regionTotals(res.Replicas, clusters)
		for region, v := range totals {
			if v > maxReplicas {
				t.Fatalf("iter %d: region %s exceeds bound", iter, region)
			}
		}
	}
}

// TestRebalanceMinGroupsMinimalEvictionsBruteForce proves minimal migration
// under the minimum-domain-coverage constraint.
func TestRebalanceMinGroupsMinimalEvictionsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	for iter := 0; iter < 250; iter++ {
		regionCount := 2 + rng.Intn(3)
		var clusters []Cluster
		existing := map[string]int64{}
		var totalCap int64
		for r := 0; r < regionCount; r++ {
			capacity := int64(rng.Intn(4))
			name := "c" + string(rune('a'+r))
			clusters = append(clusters, Cluster{
				Name:     name,
				Region:   "r" + string(rune('a'+r)),
				Weight:   int64(1 + rng.Intn(3)),
				Capacity: capacity,
			})
			totalCap += capacity
			if rng.Intn(2) == 0 {
				existing[name] = int64(rng.Intn(5))
			}
		}
		replicas := int64(rng.Intn(int(totalCap) + 1))
		minGroups := 1 + rng.Intn(regionCount)
		constraint := SpreadConstraint{Mode: SpreadMinGroups, MinGroups: minGroups}

		res, err := RebalanceWithSpread(replicas, clusters, existing, KeepExisting, []SpreadConstraint{constraint})
		if err != nil {
			if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrInsufficientCapacity) {
				continue
			}
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}

		minimum, feasible := bruteForceMinEvictionsMinGroups(clusters, existing, replicas, minGroups)
		if !feasible {
			t.Fatalf("iter %d: engine returned a plan but brute force found none feasible", iter)
		}
		if res.Migrated != minimum {
			t.Fatalf("iter %d: migrated %d but brute-force minimum is %d (plan %v, existing %v)",
				iter, res.Migrated, minimum, res.Replicas, existing)
		}
		totals := regionTotals(res.Replicas, clusters)
		used := 0
		for _, v := range totals {
			if v > 0 {
				used++
			}
		}
		if used < minGroups {
			t.Fatalf("iter %d: minGroups violated: %v", iter, totals)
		}
		assertPlanWithinCaps(t, clusters, res.Replicas)
	}
}

// bruteForceMinEvictionsMinGroups enumerates capped plans that use at least
// minGroups regions and returns the smallest achievable eviction count.
func bruteForceMinEvictionsMinGroups(clusters []Cluster, existing map[string]int64, replicas int64, minGroups int) (int64, bool) {
	best := int64(1 << 62)
	found := false
	var enumerate func(idx int, remaining int64, plan, regionSum map[string]int64)
	enumerate = func(idx int, remaining int64, plan, regionSum map[string]int64) {
		if idx == len(clusters) {
			if remaining != 0 {
				return
			}
			used := 0
			for _, v := range regionSum {
				if v > 0 {
					used++
				}
			}
			if used < minGroups {
				return
			}
			var evicted int64
			for _, c := range clusters {
				if existing[c.Name] > plan[c.Name] {
					evicted += existing[c.Name] - plan[c.Name]
				}
			}
			found = true
			if evicted < best {
				best = evicted
			}
			return
		}
		c := clusters[idx]
		upper := c.Capacity
		if upper > remaining {
			upper = remaining
		}
		for v := int64(0); v <= upper; v++ {
			plan[c.Name] = v
			regionSum[c.Region] += v
			enumerate(idx+1, remaining-v, plan, regionSum)
			regionSum[c.Region] -= v
		}
	}
	enumerate(0, replicas, map[string]int64{}, map[string]int64{})
	return best, found
}

// bruteForceMinEvictionsWithRegionMax enumerates capped plans satisfying both
// conservation and the per-region upper bound.
func bruteForceMinEvictionsWithRegionMax(clusters []Cluster, existing map[string]int64, replicas, maxReplicas int64) (int64, bool) {
	best := int64(1 << 62)
	found := false
	var enumerate func(idx int, remaining int64, plan, regionSum map[string]int64)
	enumerate = func(idx int, remaining int64, plan, regionSum map[string]int64) {
		if idx == len(clusters) {
			if remaining != 0 {
				return
			}
			var evicted int64
			for _, c := range clusters {
				if existing[c.Name] > plan[c.Name] {
					evicted += existing[c.Name] - plan[c.Name]
				}
			}
			found = true
			if evicted < best {
				best = evicted
			}
			return
		}
		c := clusters[idx]
		upper := c.Capacity
		if upper > remaining {
			upper = remaining
		}
		for v := int64(0); v <= upper; v++ {
			if regionSum[c.Region]+v > maxReplicas {
				continue
			}
			plan[c.Name] = v
			regionSum[c.Region] += v
			enumerate(idx+1, remaining-v, plan, regionSum)
			regionSum[c.Region] -= v
		}
	}
	enumerate(0, replicas, map[string]int64{}, map[string]int64{})
	return best, found
}
