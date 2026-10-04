package main

import (
	"math"
	"math/rand"
	"testing"
)

// bruteForce enumerates every facility subset of size <= K over allowed nodes
// and returns the optimal cost (+Inf if infeasible).
func bruteForce(in *Input) float64 {
	allowed := []int{}
	forb := make(map[int]bool)
	for _, f := range in.Forbidden {
		forb[f] = true
	}
	for v := 0; v < in.N; v++ {
		if !forb[v] {
			allowed = append(allowed, v)
		}
	}
	// all-pairs distances via BFS on the tree
	dist := make([][]float64, in.N)
	adj := make([][]nbr, in.N)
	for _, e := range in.Edges {
		adj[e.U] = append(adj[e.U], nbr{e.V, e.W})
		adj[e.V] = append(adj[e.V], nbr{e.U, e.W})
	}
	for s := 0; s < in.N; s++ {
		dist[s] = make([]float64, in.N)
		vis := make([]bool, in.N)
		vis[s] = true
		st := []int{s}
		for len(st) > 0 {
			v := st[len(st)-1]
			st = st[:len(st)-1]
			for _, nb := range adj[v] {
				if !vis[nb.to] {
					vis[nb.to] = true
					dist[s][nb.to] = dist[s][v] + nb.w
					st = append(st, nb.to)
				}
			}
		}
	}
	var totalDemand int64
	for _, d := range in.Demands {
		totalDemand += d
	}
	best := math.Inf(1)
	kMax := in.K
	if kMax > len(allowed) {
		kMax = len(allowed)
	}
	var costOf func(sel []int) float64
	costOf = func(sel []int) float64 {
		if len(sel) == 0 {
			if totalDemand == 0 {
				return 0
			}
			return math.Inf(1)
		}
		c := 0.0
		for v := 0; v < in.N; v++ {
			d := math.Inf(1)
			for _, f := range sel {
				if dist[v][f] < d {
					d = dist[v][f]
				}
			}
			c += float64(in.Demands[v]) * d
		}
		return c
	}
	var rec func(start int, sel []int)
	rec = func(start int, sel []int) {
		if c := costOf(sel); c < best {
			best = c
		}
		if len(sel) == kMax {
			return
		}
		for i := start; i < len(allowed); i++ {
			rec(i+1, append(append([]int(nil), sel...), allowed[i]))
		}
	}
	rec(0, nil)
	return best
}

func randTree(rng *rand.Rand, n int) []Edge {
	edges := []Edge{}
	weights := []float64{0, 0, 0.5, 1, 2, 3, 5}
	for v := 1; v < n; v++ {
		p := rng.Intn(v)
		edges = append(edges, Edge{U: p, V: v, W: weights[rng.Intn(len(weights))]})
	}
	return edges
}

func TestExactVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(9)
		in := &Input{
			N:       n,
			K:       rng.Intn(5),
			Edges:   randTree(rng, n),
			Demands: make([]int64, n),
		}
		for i := range in.Demands {
			in.Demands[i] = int64(rng.Intn(6))
		}
		for v := 0; v < n; v++ {
			if rng.Float64() < 0.3 {
				in.Forbidden = append(in.Forbidden, v)
			}
		}
		out, err := solve(in, options{})
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		bf := bruteForce(in)
		if math.IsInf(bf, 1) {
			if out.Status != "infeasible" {
				t.Fatalf("iter %d: expected infeasible, got %+v", iter, out)
			}
			continue
		}
		if out.Status != "optimal" {
			t.Fatalf("iter %d: expected optimal, got %+v", iter, out)
		}
		if math.Abs(*out.TotalCost-bf) > 1e-6 {
			t.Fatalf("iter %d: cost %v != brute force %v (input %+v)", iter, *out.TotalCost, bf, in)
		}
		checkFeasible(t, in, out)
	}
}

// checkFeasible validates the output shape, the tie-broken assignment and the
// reported total cost against an independent recomputation.
func checkFeasible(t *testing.T, in *Input, out Output) {
	t.Helper()
	if len(out.Facilities) > in.K {
		t.Fatalf("too many facilities: %v", out.Facilities)
	}
	forb := make(map[int]bool)
	for _, f := range in.Forbidden {
		forb[f] = true
	}
	seen := make(map[int]bool)
	for _, f := range out.Facilities {
		if f < 0 || f >= in.N {
			t.Fatalf("facility %d out of range", f)
		}
		if forb[f] {
			t.Fatalf("facility %d is forbidden", f)
		}
		if seen[f] {
			t.Fatalf("facility %d duplicated", f)
		}
		seen[f] = true
	}
	if len(out.Assignments) != in.N {
		t.Fatalf("assignments length %d", len(out.Assignments))
	}
	// recompute distances
	adj := make([][]nbr, in.N)
	for _, e := range in.Edges {
		adj[e.U] = append(adj[e.U], nbr{e.V, e.W})
		adj[e.V] = append(adj[e.V], nbr{e.U, e.W})
	}
	dist := make([][]float64, in.N)
	for s := 0; s < in.N; s++ {
		dist[s] = make([]float64, in.N)
		vis := make([]bool, in.N)
		vis[s] = true
		st := []int{s}
		for len(st) > 0 {
			v := st[len(st)-1]
			st = st[:len(st)-1]
			for _, nb := range adj[v] {
				if !vis[nb.to] {
					vis[nb.to] = true
					dist[s][nb.to] = dist[s][v] + nb.w
					st = append(st, nb.to)
				}
			}
		}
	}
	total := 0.0
	for v := 0; v < in.N; v++ {
		a := out.Assignments[v]
		if len(out.Facilities) == 0 {
			if a != -1 {
				t.Fatalf("node %d assigned to %d with no facilities", v, a)
			}
			continue
		}
		if !seen[a] {
			t.Fatalf("node %d assigned to non-facility %d", v, a)
		}
		// verify nearest with tie-break by smaller facility id
		bestD := math.Inf(1)
		bestID := -1
		for _, f := range out.Facilities {
			d := dist[v][f]
			if d < bestD-1e-12 || (math.Abs(d-bestD) <= 1e-12 && f < bestID) {
				bestD = d
				bestID = f
			}
		}
		if a != bestID {
			t.Fatalf("node %d assigned to %d, want %d (tie-break)", v, a, bestID)
		}
		total += float64(in.Demands[v]) * dist[v][a]
	}
	if math.Abs(total-*out.TotalCost) > 1e-6 {
		t.Fatalf("reported cost %v != recomputed %v", *out.TotalCost, total)
	}
}

func TestZeroLengthEdges(t *testing.T) {
	in := &Input{
		N: 4,
		K: 1,
		Edges: []Edge{
			{U: 0, V: 1, W: 0},
			{U: 1, V: 2, W: 0},
			{U: 2, V: 3, W: 0},
		},
		Demands: []int64{2, 3, 5, 7},
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "optimal" || *out.TotalCost != 0 {
		t.Fatalf("got %+v", out)
	}
	checkFeasible(t, in, out)
	// everyone must map to the single chosen facility
	for v, a := range out.Assignments {
		if a != out.Facilities[0] {
			t.Fatalf("node %d assigned to %d", v, a)
		}
	}
}

func TestAllForbidden(t *testing.T) {
	in := &Input{
		N:         3,
		K:         2,
		Edges:     []Edge{{U: 0, V: 1, W: 1}, {U: 1, V: 2, W: 1}},
		Demands:   []int64{1, 2, 3},
		Forbidden: []int{0, 1, 2},
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "infeasible" {
		t.Fatalf("expected infeasible, got %+v", out)
	}
	// all forbidden but zero demand: trivially optimal with no facilities
	in.Demands = []int64{0, 0, 0}
	out, err = solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "optimal" || *out.TotalCost != 0 || len(out.Facilities) != 0 {
		t.Fatalf("got %+v", out)
	}
	checkFeasible(t, in, out)
}

func TestKZero(t *testing.T) {
	in := &Input{
		N:       2,
		K:       0,
		Edges:   []Edge{{U: 0, V: 1, W: 1}},
		Demands: []int64{1, 0},
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "infeasible" {
		t.Fatalf("expected infeasible, got %+v", out)
	}
}

func TestSingleNode(t *testing.T) {
	in := &Input{N: 1, K: 1, Demands: []int64{4}}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "optimal" || *out.TotalCost != 0 || len(out.Facilities) != 1 || out.Facilities[0] != 0 {
		t.Fatalf("got %+v", out)
	}
	if out.Assignments[0] != 0 {
		t.Fatalf("assignment %+v", out.Assignments)
	}
	in.Forbidden = []int{0}
	out, err = solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "infeasible" {
		t.Fatalf("expected infeasible, got %+v", out)
	}
}

func TestTieBreakAssignment(t *testing.T) {
	// path 0-1-2, unique optimal set {0,2}; node 1 is equidistant and must go
	// to the smaller facility id (0).
	in := &Input{
		N:       3,
		K:       2,
		Edges:   []Edge{{U: 0, V: 1, W: 1}, {U: 1, V: 2, W: 1}},
		Demands: []int64{5, 0, 5},
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if *out.TotalCost != 0 {
		t.Fatalf("got %+v", out)
	}
	if out.Assignments[1] != 0 {
		t.Fatalf("node 1 assigned to %d, want 0 (smaller facility id)", out.Assignments[1])
	}
	checkFeasible(t, in, out)
}

func TestForbiddenNotUsed(t *testing.T) {
	// node 1 would be the ideal hub but is forbidden
	in := &Input{
		N:         3,
		K:         1,
		Edges:     []Edge{{U: 0, V: 1, W: 1}, {U: 1, V: 2, W: 1}},
		Demands:   []int64{10, 10, 10},
		Forbidden: []int{1},
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if *out.TotalCost != 30 {
		t.Fatalf("got %+v", out)
	}
	checkFeasible(t, in, out)
}

func TestHeuristicFeasibleAndUpperBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 30; iter++ {
		n := 2 + rng.Intn(8)
		in := &Input{
			N:       n,
			K:       1 + rng.Intn(3),
			Edges:   randTree(rng, n),
			Demands: make([]int64, n),
		}
		for i := range in.Demands {
			in.Demands[i] = int64(rng.Intn(5))
		}
		in.Demands[0] = 1 + int64(rng.Intn(4))
		out, err := solve(in, options{forceHeuristic: true, budgetMs: 200})
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "feasible" || out.Optimal {
			t.Fatalf("got %+v", out)
		}
		checkFeasible(t, in, out)
		bf := bruteForce(in)
		if !math.IsInf(bf, 1) && *out.TotalCost < bf-1e-6 {
			t.Fatalf("heuristic cost %v below optimum %v", *out.TotalCost, bf)
		}
	}
}

func TestHeuristicLargerTree(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	n := 2000
	in := &Input{
		N:       n,
		K:       20,
		Edges:   randTree(rng, n),
		Demands: make([]int64, n),
	}
	for i := range in.Demands {
		in.Demands[i] = int64(rng.Intn(10))
	}
	for v := 0; v < n; v++ {
		if rng.Float64() < 0.2 {
			in.Forbidden = append(in.Forbidden, v)
		}
	}
	out, err := solve(in, options{forceHeuristic: true, budgetMs: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "feasible" {
		t.Fatalf("got %+v", out)
	}
	checkFeasible(t, in, out)
}

func TestExactOnMediumTree(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	n := 120
	in := &Input{
		N:       n,
		K:       8,
		Edges:   randTree(rng, n),
		Demands: make([]int64, n),
	}
	for i := range in.Demands {
		in.Demands[i] = int64(rng.Intn(10))
	}
	for v := 0; v < n; v++ {
		if rng.Float64() < 0.2 {
			in.Forbidden = append(in.Forbidden, v)
		}
	}
	out, err := solve(in, options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "optimal" {
		t.Fatalf("got %+v", out)
	}
	checkFeasible(t, in, out)
}
