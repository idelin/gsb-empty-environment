package facilitycover

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// Tree: 0 (root, cost 2) -> 1 (cost 3) -> 2 (cost 1); 3 (root, cost 4).
// Demands: d0=10, d1=5, d2=7.
// Facility 0 covers {d0}, 1 covers {d0,d1} (overlap on d0!), 2 covers {d2}, 3 covers {d1}.
func detInstance() *Instance {
	return &Instance{
		Budget:        6,
		DemandRevenue: []int64{10, 5, 7},
		Facilities: []Facility{
			{ID: 0, Parent: -1, Cost: 2},
			{ID: 1, Parent: 0, Cost: 3},
			{ID: 2, Parent: 1, Cost: 1},
			{ID: 3, Parent: -1, Cost: 4},
		},
		Coverage: map[int][]int{
			0: {0},
			1: {0, 1},
			2: {2},
			3: {1},
		},
	}
}

func TestDeterministicHandComputed(t *testing.T) {
	inst := detInstance()
	res, err := SolveDeterministic(inst)
	if err != nil {
		t.Fatal(err)
	}
	// Budget 6. Options:
	//  {0,1} cost 5 -> covers d0,d1 = 15 (d0 counted once despite overlap)
	//  {0,3} cost 6 -> covers d0,d1 = 15
	//  {0,1,2} cost 6 -> covers d0,d1,d2 = 22  <- best
	//  {3} cost 4 -> 5
	if res.Revenue != 22 {
		t.Fatalf("revenue = %d, want 22", res.Revenue)
	}
	if res.Cost != 6 {
		t.Fatalf("cost = %d, want 6", res.Cost)
	}
	want := []int{0, 1, 2}
	if !reflect.DeepEqual(res.Opened, want) {
		t.Fatalf("opened = %v, want %v", res.Opened, want)
	}
	if err := VerifyDeterministic(inst, res); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicOverlapNotDoubleCounted(t *testing.T) {
	// Two cheap roots both covering the same demand: revenue counts it once.
	inst := &Instance{
		Budget:        10,
		DemandRevenue: []int64{100},
		Facilities: []Facility{
			{ID: 0, Parent: -1, Cost: 1},
			{ID: 1, Parent: -1, Cost: 1},
		},
		Coverage: map[int][]int{0: {0}, 1: {0}},
	}
	res, err := SolveDeterministic(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revenue != 100 {
		t.Fatalf("revenue = %d, want 100 (no double counting)", res.Revenue)
	}
	// Tie-break: minimal cost -> open only one of them.
	if res.Cost != 1 || len(res.Opened) != 1 {
		t.Fatalf("opened = %v cost = %d, want a single facility", res.Opened, res.Cost)
	}
	if err := VerifyDeterministic(inst, res); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicClosureForced(t *testing.T) {
	// Child is cheap and valuable but requires an expensive ancestor chain.
	inst := &Instance{
		Budget:        5,
		DemandRevenue: []int64{50},
		Facilities: []Facility{
			{ID: 0, Parent: -1, Cost: 4},
			{ID: 1, Parent: 0, Cost: 4}, // covers the demand, but 0+1 = 8 > 5
		},
		Coverage: map[int][]int{1: {0}},
	}
	res, err := SolveDeterministic(inst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revenue != 0 || len(res.Opened) != 0 {
		t.Fatalf("opened = %v revenue = %d, want nothing open", res.Opened, res.Revenue)
	}
	if err := VerifyDeterministic(inst, res); err != nil {
		t.Fatal(err)
	}
}

func randomInstance(r *rand.Rand, n, d int, budget int64) *Instance {
	inst := &Instance{Budget: budget}
	for i := 0; i < d; i++ {
		inst.DemandRevenue = append(inst.DemandRevenue, int64(1+r.Intn(10)))
	}
	inst.Coverage = map[int][]int{}
	for i := 0; i < n; i++ {
		parent := -1
		if i > 0 && r.Intn(2) == 0 {
			parent = r.Intn(i) // parent always has smaller ID: a forest
		}
		inst.Facilities = append(inst.Facilities, Facility{
			ID: i, Parent: parent, Cost: int64(1 + r.Intn(6)),
		})
		var cov []int
		for j := 0; j < d; j++ {
			if r.Intn(3) == 0 {
				cov = append(cov, j)
			}
		}
		inst.Coverage[i] = cov
	}
	return inst
}

func TestDeterministicRandomCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + r.Intn(10)
		d := 1 + r.Intn(8)
		inst := randomInstance(r, n, d, int64(r.Intn(15)))
		res, err := SolveDeterministic(inst)
		if err != nil {
			t.Fatal(err)
		}
		// VerifyDeterministic brute-forces all 2^n subsets independently.
		if err := VerifyDeterministic(inst, res); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestDeterministicMaxSize(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	inst := randomInstance(r, MaxDeterministicFacilities, 40, 30)
	res, err := SolveDeterministic(inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeterministic(inst, res); err != nil {
		t.Fatal(err)
	}
}

// --- Robust problem ---

// 3 facilities in a chain 0->1->2, costs 2/1/1, budget 4, emergency limit 1.
// Two scenarios in different groups; coverage differs per scenario.
func robustInstance() *RobustInstance {
	return &RobustInstance{
		Instance: Instance{
			Budget:        4,
			DemandRevenue: []int64{10, 8, 6},
			Facilities: []Facility{
				{ID: 0, Parent: -1, Cost: 2},
				{ID: 1, Parent: 0, Cost: 1},
				{ID: 2, Parent: 1, Cost: 1},
			},
		},
		EmergencyLimit: 1,
		Scenarios: []Scenario{
			{Group: 0, Probability: 0.5, Coverage: map[int][]int{0: {0}, 1: {1}, 2: {2}}},
			{Group: 1, Probability: 0.5, Coverage: map[int][]int{0: {1}, 1: {0}, 2: {2}}},
		},
	}
}

func TestRobustHandComputed(t *testing.T) {
	inst := robustInstance()
	res, err := SolveRobust(inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRobust(inst, res); err != nil {
		t.Fatal(err)
	}
	// Stage 1 must open {0,1} (cost 3) so both 1 and 2 are emergency-reachable;
	// then one emergency facility per group covers the missing high-value demand.
	// Scenario 0 with open {0,1}: covers d0,d1 = 18; emergency 2 adds d2 -> 24.
	// Scenario 1 with open {0,1}: covers d0,d1 = 18; emergency 2 adds d2 -> 24.
	// Worst = 24, cost per scenario = 4 <= budget, expected cost = 4.
	if res.WorstRevenue != 24 {
		t.Fatalf("worst revenue = %d, want 24", res.WorstRevenue)
	}
	if !reflect.DeepEqual(res.Opened, []int{0, 1}) {
		t.Fatalf("stage-1 opened = %v, want [0 1]", res.Opened)
	}
	if !reflect.DeepEqual(res.Emergency[0], []int{2}) || !reflect.DeepEqual(res.Emergency[1], []int{2}) {
		t.Fatalf("emergency = %v, want both groups to enable [2]", res.Emergency)
	}
	if math.Abs(res.ExpectedCost-4) > 1e-9 {
		t.Fatalf("expected cost = %g, want 4", res.ExpectedCost)
	}
	for s := range inst.Scenarios {
		if res.ScenarioCost[s] > inst.Budget {
			t.Fatalf("scenario %d over budget: %d", s, res.ScenarioCost[s])
		}
	}
}

func TestRobustSameGroupSameAction(t *testing.T) {
	inst := robustInstance()
	// Put both scenarios in one observation group: a single emergency action
	// must serve both, so the worst case can no longer reach 24 for both
	// with a tailored action... here facility 2 helps both, so worst stays 24,
	// but the emergency map must contain exactly one shared action.
	inst.Scenarios[1].Group = 0
	res, err := SolveRobust(inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRobust(inst, res); err != nil {
		t.Fatal(err)
	}
	if len(res.Emergency) != 1 {
		t.Fatalf("expected a single group action, got %v", res.Emergency)
	}
	if res.WorstRevenue != 24 {
		t.Fatalf("worst revenue = %d, want 24", res.WorstRevenue)
	}
}

func randomRobustInstance(r *rand.Rand, n, d, ns, nGroups int, budget int64, limit int) *RobustInstance {
	inst := &RobustInstance{Instance: *randomInstance(r, n, d, budget), EmergencyLimit: limit}
	for s := 0; s < ns; s++ {
		sc := Scenario{Group: r.Intn(nGroups), Probability: 1.0 / float64(ns), Coverage: map[int][]int{}}
		for i := 0; i < n; i++ {
			var cov []int
			for j := 0; j < d; j++ {
				if r.Intn(3) == 0 {
					cov = append(cov, j)
				}
			}
			sc.Coverage[i] = cov
		}
		inst.Scenarios = append(inst.Scenarios, sc)
	}
	return inst
}

// bruteForceRobust is an independent, deliberately naive enumerator used only
// in tests: all stage-1 subsets x all per-group emergency subsets.
func bruteForceRobust(t *testing.T, inst *RobustInstance) (int64, float64) {
	t.Helper()
	p, err := prepare(&inst.Instance, inst.Instance.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	ns := len(inst.Scenarios)
	scenPrep := make([]*prepared, ns)
	for s := range inst.Scenarios {
		scenPrep[s], err = prepare(&inst.Instance, inst.Scenarios[s].Coverage)
		if err != nil {
			t.Fatal(err)
		}
	}
	groups := map[int][]int{}
	for s, sc := range inst.Scenarios {
		groups[sc.Group] = append(groups[sc.Group], s)
	}
	bestWorst := int64(-1)
	bestCost := math.Inf(1)
	for mask := 0; mask < (1 << p.n); mask++ {
		open := make([]bool, p.n)
		for i := 0; i < p.n; i++ {
			open[i] = mask&(1<<i) != 0
		}
		if !p.isClosure(open) {
			continue
		}
		stageCost := p.costOf(open)
		if stageCost > inst.Budget {
			continue
		}
		var cand []int
		for i := 0; i < p.n; i++ {
			if !open[i] && (p.parentIdx[i] == -1 || open[p.parentIdx[i]]) {
				cand = append(cand, i)
			}
		}
		// Per group: best achievable (groupWorst, min emergency cost).
		worst := int64(-1)
		expected := float64(stageCost)
		feasible := true
		for _, scenList := range groups {
			gWorst := int64(-1)
			gCost := int64(math.MaxInt64)
			for em := 0; em < (1 << len(cand)); em++ {
				if popcount(em) > inst.EmergencyLimit {
					continue
				}
				var ec int64
				use := make([]bool, p.n)
				copy(use, open)
				for j, ci := range cand {
					if em&(1<<j) != 0 {
						ec += p.costs[ci]
						use[ci] = true
					}
				}
				if stageCost+ec > inst.Budget {
					continue
				}
				w := int64(-1)
				for _, s := range scenList {
					u := scenPrep[s].unionCoverage(scenPrep[s].cov, use)
					rev := revenueOf(u, inst.DemandRevenue)
					if w == -1 || rev < w {
						w = rev
					}
				}
				if w > gWorst || (w == gWorst && ec < gCost) {
					gWorst, gCost = w, ec
				}
			}
			if gWorst == -1 {
				feasible = false
				break
			}
			if worst == -1 || gWorst < worst {
				worst = gWorst
			}
			var gp float64
			for _, s := range scenList {
				gp += inst.Scenarios[s].Probability
			}
			expected += gp * float64(gCost)
		}
		if !feasible {
			continue
		}
		if worst > bestWorst || (worst == bestWorst && expected < bestCost) {
			bestWorst, bestCost = worst, expected
		}
	}
	return bestWorst, bestCost
}

func popcount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

func TestRobustRandomCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		n := 1 + r.Intn(7)
		d := 1 + r.Intn(6)
		ns := 1 + r.Intn(3)
		ng := 1 + r.Intn(ns)
		inst := randomRobustInstance(r, n, d, ns, ng, int64(r.Intn(14)), r.Intn(3))
		res, err := SolveRobust(inst)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyRobust(inst, res); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		wantWorst, wantCost := bruteForceRobust(t, inst)
		if res.WorstRevenue != wantWorst {
			t.Fatalf("trial %d: worst = %d, brute force = %d", trial, res.WorstRevenue, wantWorst)
		}
		if math.Abs(res.ExpectedCost-wantCost) > 1e-9 {
			t.Fatalf("trial %d: expected cost = %g, brute force = %g", trial, res.ExpectedCost, wantCost)
		}
	}
}

func TestRobustMaxSize(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	inst := randomRobustInstance(r, MaxRobustFacilities, 30, MaxRobustScenarios, 3, 25, 2)
	res, err := SolveRobust(inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRobust(inst, res); err != nil {
		t.Fatal(err)
	}
}
