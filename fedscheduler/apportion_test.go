package fedscheduler

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

func planSum(plan map[string]int64) int64 {
	var s int64
	for _, v := range plan {
		s += v
	}
	return s
}

func assertPlanWithinCaps(t *testing.T, clusters []Cluster, plan map[string]int64) {
	t.Helper()
	for _, c := range clusters {
		if plan[c.Name] < 0 || plan[c.Name] > c.Capacity {
			t.Fatalf("cluster %s allocation %d outside [0,%d]", c.Name, plan[c.Name], c.Capacity)
		}
	}
}

func TestAllocateProportionalAndConserved(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 100},
		{Name: "c2", Weight: 1, Capacity: 100},
		{Name: "c3", Weight: 1, Capacity: 100},
	}
	res, err := Allocate(10, clusters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int64{"c1": 4, "c2": 3, "c3": 3}
	if !mapsEqual(res.Replicas, want) {
		t.Fatalf("got %v, want %v", res.Replicas, want)
	}
	if planSum(res.Replicas) != 10 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
}

func TestAllocateMultiRoundCapOverflow(t *testing.T) {
	// c1 saturates first; its overflow cascades through c2 until c2 also
	// saturates, leaving c3 to absorb the rest.
	clusters := []Cluster{
		{Name: "c1", Weight: 5, Capacity: 2},
		{Name: "c2", Weight: 3, Capacity: 2},
		{Name: "c3", Weight: 2, Capacity: 100},
	}
	res, err := Allocate(20, clusters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int64{"c1": 2, "c2": 2, "c3": 16}
	if !mapsEqual(res.Replicas, want) {
		t.Fatalf("got %v, want %v", res.Replicas, want)
	}
	if planSum(res.Replicas) != 20 {
		t.Fatalf("conservation broken: %v", res.Replicas)
	}
	assertPlanWithinCaps(t, clusters, res.Replicas)
	if res.Rounds < 2 {
		t.Fatalf("expected multiple redistribution rounds, got %d", res.Rounds)
	}
	if !slicesEqual(res.Saturated, []string{"c1", "c2"}) {
		t.Fatalf("saturated = %v, want [c1 c2]", res.Saturated)
	}
}

func TestAllocateLRMDeterministicTieBreak(t *testing.T) {
	// Equal weights, 10 units over 4 clusters: floors are 2 each and two
	// remainder units must go to the lexicographically first names.
	clusters := []Cluster{
		{Name: "delta", Weight: 1, Capacity: 10},
		{Name: "alpha", Weight: 1, Capacity: 10},
		{Name: "charlie", Weight: 1, Capacity: 10},
		{Name: "bravo", Weight: 1, Capacity: 10},
	}
	res, err := Allocate(10, clusters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int64{"alpha": 3, "bravo": 3, "charlie": 2, "delta": 2}
	if !mapsEqual(res.Replicas, want) {
		t.Fatalf("got %v, want %v", res.Replicas, want)
	}

	// Repeated calls and shuffled input order must produce the same plan.
	shuffled := []Cluster{clusters[2], clusters[0], clusters[3], clusters[1]}
	res2, err := Allocate(10, shuffled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mapsEqual(res.Replicas, res2.Replicas) {
		t.Fatalf("non-deterministic allocation: %v vs %v", res.Replicas, res2.Replicas)
	}
}

func TestAllocateMatchesReferenceLRM(t *testing.T) {
	// With caps large enough to never bind, the recursive engine must match
	// an independently written textbook largest-remainder (Hamilton) method.
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(8)
		clusters := make([]Cluster, n)
		weights := make(map[string]int64, n)
		for i := 0; i < n; i++ {
			name := "cluster-" + string(rune('a'+i))
			w := int64(1 + rng.Intn(20))
			clusters[i] = Cluster{Name: name, Weight: w, Capacity: 1_000_000}
			weights[name] = w
		}
		replicas := int64(rng.Intn(500))
		res, err := Allocate(replicas, clusters)
		if err != nil {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
		want := referenceLRM(weights, replicas)
		if !mapsEqual(res.Replicas, want) {
			t.Fatalf("iter %d: got %v, reference LRM %v", iter, res.Replicas, want)
		}
	}
}

// referenceLRM is a straightforward textbook implementation of the
// largest-remainder method used only as an independent oracle in tests.
func referenceLRM(weights map[string]int64, replicas int64) map[string]int64 {
	names := make([]string, 0, len(weights))
	var weightSum int64
	for name, w := range weights {
		names = append(names, name)
		weightSum += w
	}
	sortStrings(names)

	out := make(map[string]int64, len(names))
	type rem struct {
		name string
		val  int64
	}
	rems := make([]rem, 0, len(names))
	var assigned int64
	for _, name := range names {
		num := replicas * weights[name]
		floor, remainder := num/weightSum, num%weightSum
		out[name] = floor
		assigned += floor
		rems = append(rems, rem{name: name, val: remainder})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		fracI := float64(rems[i].val) / float64(weights[rems[i].name])
		fracJ := float64(rems[j].val) / float64(weights[rems[j].name])
		if fracI != fracJ {
			return fracI > fracJ
		}
		return rems[i].name < rems[j].name
	})
	for k := int64(0); k < replicas-assigned; k++ {
		out[rems[k].name]++
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func TestAllocateZeroReplicasAndZeroCap(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 1, Capacity: 0},
		{Name: "c2", Weight: 2, Capacity: 5},
	}
	res, err := Allocate(0, clusters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Replicas["c1"] != 0 || res.Replicas["c2"] != 0 || res.Total != 0 {
		t.Fatalf("zero request must yield zero plan, got %v", res.Replicas)
	}

	res, err = Allocate(5, clusters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Replicas["c1"] != 0 || res.Replicas["c2"] != 5 {
		t.Fatalf("zero-cap cluster must stay empty, got %v", res.Replicas)
	}
}

func TestAllocateInsufficientCapacityDiagnostic(t *testing.T) {
	clusters := []Cluster{
		{Name: "c1", Weight: 5, Capacity: 3},
		{Name: "c2", Weight: 3, Capacity: 2},
		{Name: "c3", Weight: 2, Capacity: 0},
	}
	_, err := Allocate(10, clusters)
	if err == nil {
		t.Fatal("expected insufficient capacity error")
	}
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("error must match ErrInsufficientCapacity, got %v", err)
	}
	var ice *InsufficientCapacityError
	if !errors.As(err, &ice) {
		t.Fatalf("error must be *InsufficientCapacityError, got %T", err)
	}
	if ice.Requested != 10 || ice.Capacity != 5 || ice.Deficit != 5 {
		t.Fatalf("unexpected diagnostic: %+v", ice)
	}
	wantPlan := map[string]int64{"c1": 3, "c2": 2, "c3": 0}
	if !mapsEqual(ice.MaxPlan, wantPlan) {
		t.Fatalf("max plan = %v, want %v", ice.MaxPlan, wantPlan)
	}
}

func TestAllocateInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		replicas int64
		clusters []Cluster
	}{
		{"negative replicas", -1, []Cluster{{Name: "c", Weight: 1, Capacity: 1}}},
		{"no clusters", 3, nil},
		{"empty name", 3, []Cluster{{Name: "", Weight: 1, Capacity: 1}}},
		{"duplicate name", 3, []Cluster{
			{Name: "c", Weight: 1, Capacity: 1},
			{Name: "c", Weight: 1, Capacity: 1},
		}},
		{"zero weight", 3, []Cluster{{Name: "c", Weight: 0, Capacity: 1}}},
		{"negative capacity", 3, []Cluster{{Name: "c", Weight: 1, Capacity: -1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Allocate(tc.replicas, tc.clusters); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAllocateRandomizedInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		n := 1 + rng.Intn(6)
		clusters := make([]Cluster, n)
		var totalCap int64
		for i := range clusters {
			capacity := int64(rng.Intn(5))
			clusters[i] = Cluster{
				Name:     "c" + string(rune('a'+i)),
				Weight:   int64(1 + rng.Intn(9)),
				Capacity: capacity,
			}
			totalCap += capacity
		}
		replicas := int64(rng.Intn(int(totalCap) + 1))
		res, err := Allocate(replicas, clusters)
		if totalCap < replicas {
			if !errors.Is(err, ErrInsufficientCapacity) {
				t.Fatalf("iter %d: expected capacity error, got %v", iter, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
		if planSum(res.Replicas) != replicas {
			t.Fatalf("iter %d: conservation broken %v", iter, res.Replicas)
		}
		assertPlanWithinCaps(t, clusters, res.Replicas)

		// Every capped allocation of the same total is a feasible plan; the
		// engine must at least produce one, and repeated calls must be stable.
		feasible := enumerateFeasiblePlans(clusters, replicas)
		if len(feasible) == 0 {
			t.Fatalf("iter %d: no feasible plan exists but allocation succeeded", iter)
		}
		if !containsPlan(feasible, res.Replicas) {
			t.Fatalf("iter %d: plan %v is not among feasible allocations", iter, res.Replicas)
		}
		res2, err := Allocate(replicas, shuffledCopy(clusters))
		if err != nil || !mapsEqual(res.Replicas, res2.Replicas) {
			t.Fatalf("iter %d: non-deterministic result %v vs %v", iter, res.Replicas, res2)
		}
	}
}

// enumerateFeasiblePlans returns every capped allocation whose values sum to
// replicas. It is only used on tiny randomized inputs.
func enumerateFeasiblePlans(clusters []Cluster, replicas int64) []map[string]int64 {
	var out []map[string]int64
	var enumerate func(idx int, remaining int64, plan map[string]int64)
	enumerate = func(idx int, remaining int64, plan map[string]int64) {
		if idx == len(clusters)-1 {
			if remaining <= clusters[idx].Capacity {
				plan[clusters[idx].Name] = remaining
				out = append(out, cloneMap(plan))
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
	return out
}

func containsPlan(plans []map[string]int64, target map[string]int64) bool {
	for _, p := range plans {
		if mapsEqual(p, target) {
			return true
		}
	}
	return false
}

func shuffledCopy(clusters []Cluster) []Cluster {
	out := append([]Cluster(nil), clusters...)
	rng := rand.New(rand.NewSource(7))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
