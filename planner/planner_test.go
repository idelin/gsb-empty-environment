package planner

import (
	"math"
	"math/rand"
	"testing"
)

func mustBuild(t *testing.T, plan Plan) *Problem {
	t.Helper()
	p, err := BuildProblem(plan)
	if err != nil {
		t.Fatalf("BuildProblem: %v", err)
	}
	return p
}

func orderOf(t *testing.T, p *Problem, ids ...string) []int {
	t.Helper()
	order := make([]int, len(ids))
	for i, id := range ids {
		idx, ok := p.IndexOf(id)
		if !ok {
			t.Fatalf("unknown block %q", id)
		}
		order[i] = idx
	}
	return order
}

func simpleBlock(id string, deps []string, dl, tmp, fin int64) Block {
	return Block{
		ID:       id,
		Deps:     deps,
		Download: Stage{Size: dl},
		Temp:     Stage{Size: tmp},
		Final:    Stage{Size: fin},
	}
}

func TestSimulatePeakExceedsFinal(t *testing.T) {
	p := mustBuild(t, Plan{Blocks: []Block{
		simpleBlock("a", nil, 10, 20, 5),
		simpleBlock("b", nil, 10, 20, 5),
	}})
	sim, err := p.Simulate(orderOf(t, p, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	// a: dl=10, +tmp=30, -dl=20, +fin=25, -tmp=5
	// b: dl=15, +tmp=35, -dl=25, +fin=30, -tmp=10
	if sim.Peak != 35 {
		t.Fatalf("peak = %d, want 35", sim.Peak)
	}
	if sim.Final != 10 {
		t.Fatalf("final = %d, want 10", sim.Final)
	}
	if sim.Peak == sim.Final {
		t.Fatal("peak must not be equated with final size")
	}
}

func TestSharedChunksCountedOnce(t *testing.T) {
	p := mustBuild(t, Plan{Blocks: []Block{
		{
			ID:       "a",
			Download: Stage{Size: 4},
			Temp:     Stage{Size: 6},
			Final: Stage{Chunks: []Chunk{
				{Name: "shared", Size: 10},
				{Name: "a-only", Size: 1},
			}},
		},
		{
			ID:       "b",
			Deps:     []string{"a"},
			Download: Stage{Size: 2},
			Temp:     Stage{Size: 3},
			Final: Stage{Chunks: []Chunk{
				{Name: "shared", Size: 10},
				{Name: "b-only", Size: 2},
			}},
		},
	}})
	sim, err := p.Simulate(orderOf(t, p, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if sim.Final != 13 {
		t.Fatalf("final = %d, want 13 (shared chunk counted once)", sim.Final)
	}
	// End-of-plan set is 13, but block "a" alone peaks at 4+6+11=17.
	if lb := p.StaticLowerBound(); lb != 17 {
		t.Fatalf("static lower bound = %d, want 17", lb)
	}
}

func TestReleaseAfterDependents(t *testing.T) {
	p := mustBuild(t, Plan{Blocks: []Block{
		{
			ID:       "base",
			Download: Stage{Size: 5},
			Temp:     Stage{Size: 5},
			Final:    Stage{Size: 50, Release: ReleaseAfterDependents},
		},
		{
			ID:       "app",
			Deps:     []string{"base"},
			Download: Stage{Size: 2},
			Temp:     Stage{Size: 2},
			Final:    Stage{Size: 1},
		},
	}})
	sim, err := p.Simulate(orderOf(t, p, "base", "app"))
	if err != nil {
		t.Fatal(err)
	}
	// base's final is freed once app (its only dependent) is published.
	if sim.Final != 1 {
		t.Fatalf("final = %d, want 1", sim.Final)
	}
	if sim.Peak < 50 {
		t.Fatalf("peak = %d, want at least 50", sim.Peak)
	}
}

func TestSimulateRejectsBadOrder(t *testing.T) {
	p := mustBuild(t, Plan{Blocks: []Block{
		simpleBlock("a", nil, 1, 1, 1),
		simpleBlock("b", []string{"a"}, 1, 1, 1),
	}})
	if _, err := p.Simulate(orderOf(t, p, "b", "a")); err == nil {
		t.Fatal("expected error for non-topological order")
	}
	if _, err := p.Simulate(orderOf(t, p, "a", "a")); err == nil {
		t.Fatal("expected error for duplicate block")
	}
	if _, err := p.Simulate(orderOf(t, p, "a")); err == nil {
		t.Fatal("expected error for incomplete order")
	}
}

func TestBuildValidation(t *testing.T) {
	cases := []struct {
		name string
		plan Plan
	}{
		{"cycle", Plan{Blocks: []Block{
			simpleBlock("a", []string{"b"}, 1, 1, 1),
			simpleBlock("b", []string{"a"}, 1, 1, 1),
		}}},
		{"unknown dep", Plan{Blocks: []Block{
			simpleBlock("a", []string{"nope"}, 1, 1, 1),
		}}},
		{"duplicate id", Plan{Blocks: []Block{
			simpleBlock("a", nil, 1, 1, 1),
			simpleBlock("a", nil, 1, 1, 1),
		}}},
		{"self dependency", Plan{Blocks: []Block{
			simpleBlock("a", []string{"a"}, 1, 1, 1),
		}}},
		{"conflicting shared chunk size", Plan{Blocks: []Block{
			{ID: "a", Final: Stage{Chunks: []Chunk{{Name: "s", Size: 5}}}},
			{ID: "b", Final: Stage{Chunks: []Chunk{{Name: "s", Size: 6}}}},
		}}},
		{"size and chunks both set", Plan{Blocks: []Block{
			{ID: "a", Final: Stage{Size: 5, Chunks: []Chunk{{Name: "s", Size: 5}}}},
		}}},
		{"bad release point", Plan{Blocks: []Block{
			{ID: "a", Final: Stage{Size: 5, Release: "whenever"}},
		}}},
	}
	for _, tc := range cases {
		if _, err := BuildProblem(tc.plan); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
}

// bruteForce enumerates every topological order and returns the minimum peak.
func bruteForce(t *testing.T, p *Problem) int64 {
	t.Helper()
	n := p.NumBlocks()
	best := int64(math.MaxInt64)
	scheduled := make([]bool, n)
	order := make([]int, 0, n)
	var rec func()
	rec = func() {
		if len(order) == n {
			sim, err := p.Simulate(order)
			if err != nil {
				t.Fatal(err)
			}
			if sim.Peak < best {
				best = sim.Peak
			}
			return
		}
		for b := 0; b < n; b++ {
			if scheduled[b] {
				continue
			}
			ok := true
			for _, d := range p.deps[b] {
				if !scheduled[d] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			scheduled[b] = true
			order = append(order, b)
			rec()
			order = order[:len(order)-1]
			scheduled[b] = false
		}
	}
	rec()
	return best
}

func randomPlan(r *rand.Rand, n int) Plan {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	// Shuffle ids so input order is not already topological.
	r.Shuffle(n, func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	// pos[id] = position in a fixed topological order.
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	releases := []ReleasePoint{ReleaseOnDecompress, ReleaseOnPublish, ReleaseAfterDependents, ReleaseEndOfPlan}
	plan := Plan{}
	for _, id := range ids {
		var deps []string
		for _, other := range ids {
			if pos[other] < pos[id] && r.Float64() < 0.35 {
				deps = append(deps, other)
			}
		}
		blk := simpleBlock(id, deps,
			int64(1+r.Intn(15)), int64(1+r.Intn(15)), int64(1+r.Intn(15)))
		if r.Float64() < 0.3 {
			blk.Final.Release = releases[r.Intn(len(releases))]
		}
		if r.Float64() < 0.2 {
			blk.Download.Release = releases[r.Intn(len(releases))]
		}
		plan.Blocks = append(plan.Blocks, blk)
	}
	return plan
}

func TestSolveMatchesBruteForce(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 4 + r.Intn(4) // 4..7 blocks
		p := mustBuild(t, randomPlan(r, n))
		res := p.Solve(Limits{})
		if res.Interrupted {
			t.Fatalf("seed %d: unlimited search interrupted", seed)
		}
		want := bruteForce(t, p)
		if res.Peak != want {
			t.Fatalf("seed %d: peak = %d, brute force = %d", seed, res.Peak, want)
		}
		if res.LowerBound != want || res.UpperBound != want {
			t.Fatalf("seed %d: bounds [%d, %d], want both %d", seed, res.LowerBound, res.UpperBound, want)
		}
		if res.Status != StatusOptimal {
			t.Fatalf("seed %d: status = %s, want optimal", seed, res.Status)
		}
		assertValidOrder(t, p, res.Order)
	}
}

func TestGreedyIsFeasibleUpperBound(t *testing.T) {
	for seed := int64(100); seed < 120; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := mustBuild(t, randomPlan(r, 6))
		order, peak := p.Greedy()
		if len(order) != p.NumBlocks() {
			t.Fatalf("greedy order length %d", len(order))
		}
		sim, err := p.Simulate(order)
		if err != nil {
			t.Fatalf("greedy order invalid: %v", err)
		}
		if sim.Peak != peak {
			t.Fatalf("greedy peak %d, simulated %d", peak, sim.Peak)
		}
		if opt := bruteForce(t, p); peak < opt {
			t.Fatalf("greedy peak %d below optimum %d (impossible)", peak, opt)
		}
	}
}

func TestInterruptReturnsLegalBounds(t *testing.T) {
	for seed := int64(200); seed < 210; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := mustBuild(t, randomPlan(r, 9))
		res := p.Solve(Limits{MaxExpanded: 64})
		if !res.Interrupted {
			continue // tiny DAGs may finish; bounds still checked below
		}
		opt := bruteForce(t, p)
		if res.LowerBound > opt {
			t.Fatalf("seed %d: lower bound %d exceeds true optimum %d (illegal)", seed, res.LowerBound, opt)
		}
		if res.UpperBound < opt {
			t.Fatalf("seed %d: upper bound %d below true optimum %d (illegal)", seed, res.UpperBound, opt)
		}
		if res.LowerBound > res.UpperBound {
			t.Fatalf("seed %d: lower %d > upper %d", seed, res.LowerBound, res.UpperBound)
		}
		if lb := p.StaticLowerBound(); res.LowerBound < lb {
			t.Fatalf("seed %d: lower bound %d below static bound %d", seed, res.LowerBound, lb)
		}
		assertValidOrder(t, p, res.Order)
		sim, err := p.Simulate(mustOrder(t, p, res.Order))
		if err != nil {
			t.Fatal(err)
		}
		if sim.Peak != res.UpperBound {
			t.Fatalf("seed %d: upper bound %d but plan peaks at %d", seed, res.UpperBound, sim.Peak)
		}
	}
}

func TestStatusClassification(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	plan := randomPlan(r, 9)
	plan.Budget = 1 // impossibly small
	p := mustBuild(t, plan)
	res := p.Solve(Limits{})
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}

	plan.Budget = math.MaxInt64 // always fits
	p = mustBuild(t, plan)
	res = p.Solve(Limits{MaxExpanded: 8})
	if res.Interrupted && res.Status != StatusFeasible {
		t.Fatalf("status = %s, want feasible", res.Status)
	}
}

func assertValidOrder(t *testing.T, p *Problem, ids []string) {
	t.Helper()
	if len(ids) != p.NumBlocks() {
		t.Fatalf("order has %d blocks, want %d", len(ids), p.NumBlocks())
	}
	if _, err := p.Simulate(mustOrder(t, p, ids)); err != nil {
		t.Fatalf("returned order invalid: %v", err)
	}
}

func mustOrder(t *testing.T, p *Problem, ids []string) []int {
	t.Helper()
	return orderOf(t, p, ids...)
}
