package solver

import (
	"math/rand"
	"testing"
)

func cutSet(p *Plan) map[int]bool {
	m := make(map[int]bool)
	for _, c := range p.Cuts {
		m[c.EdgeID] = true
	}
	return m
}

func TestSimpleCycleCheapestCut(t *testing.T) {
	g := &Graph{
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c", Cuttable: true, Cost: 7, Artifact: "boot-bc"},
			{From: "c", To: "a", Cuttable: true, Cost: 2, Artifact: "boot-ca"},
		},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Feasible {
		t.Fatalf("expected feasible, got %+v", p)
	}
	if p.TotalCost != 2 {
		t.Fatalf("want cost 2, got %d", p.TotalCost)
	}
	if len(p.Cuts) != 1 || p.Cuts[0].Artifact != "boot-ca" || p.Cuts[0].From != "c" || p.Cuts[0].To != "a" {
		t.Fatalf("wrong cut: %+v", p.Cuts)
	}
	if len(p.TopoOrder) != 3 {
		t.Fatalf("topo order missing nodes: %v", p.TopoOrder)
	}
}

func TestUnbreakableCycleInfeasible(t *testing.T) {
	g := &Graph{
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
			{From: "c", To: "a"},
		},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if p.Feasible {
		t.Fatalf("expected infeasible, got %+v", p)
	}
}

func TestSelfLoop(t *testing.T) {
	g := &Graph{
		Edges: []Edge{
			{From: "a", To: "a", Cuttable: true, Cost: 5, Artifact: "boot-aa"},
			{From: "a", To: "b"},
		},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Feasible || p.TotalCost != 5 || len(p.Cuts) != 1 || p.Cuts[0].Artifact != "boot-aa" {
		t.Fatalf("bad plan: %+v", p)
	}
}

func TestUncuttableSelfLoopInfeasible(t *testing.T) {
	g := &Graph{Edges: []Edge{{From: "a", To: "a"}}}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if p.Feasible {
		t.Fatalf("expected infeasible, got %+v", p)
	}
}

func TestSharedCycleEdgeBeatsTwoCheapCuts(t *testing.T) {
	// Two cycles share edge d->a. Cutting the shared edge (cost 5) breaks
	// both; cutting the private edges costs 3+3=6.
	g := &Graph{
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "d", Cuttable: true, Cost: 3, Artifact: "boot-bd"},
			{From: "a", To: "c"},
			{From: "c", To: "d", Cuttable: true, Cost: 3, Artifact: "boot-cd"},
			{From: "d", To: "a", Cuttable: true, Cost: 5, Artifact: "boot-da"},
		},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Feasible || p.TotalCost != 5 {
		t.Fatalf("want single cost-5 cut, got %+v", p)
	}
	if len(p.Cuts) != 1 || p.Cuts[0].Artifact != "boot-da" {
		t.Fatalf("wrong cuts: %+v", p.Cuts)
	}
}

func TestAcyclicNeedsNoCuts(t *testing.T) {
	g := &Graph{
		Nodes: []string{"x", "y", "z"},
		Edges: []Edge{{From: "x", To: "y"}, {From: "y", To: "z"}},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Feasible || p.TotalCost != 0 || len(p.Cuts) != 0 {
		t.Fatalf("bad plan: %+v", p)
	}
	want := []string{"x", "y", "z"}
	for i, n := range want {
		if p.TopoOrder[i] != n {
			t.Fatalf("topo order = %v, want %v", p.TopoOrder, want)
		}
	}
}

func TestTopoOrderRespectsRemainingEdges(t *testing.T) {
	g := &Graph{
		Edges: []Edge{
			{From: "build", To: "test"},
			{From: "test", To: "deploy"},
			{From: "deploy", To: "build", Cuttable: true, Cost: 1, Artifact: "boot-build"},
			{From: "lint", To: "test"},
		},
	}
	p, err := Solve(g)
	if err != nil {
		t.Fatal(err)
	}
	pos := make(map[string]int)
	for i, n := range p.TopoOrder {
		pos[n] = i
	}
	for _, e := range g.Edges {
		if cutSet(p)[e.ID] {
			continue
		}
		if pos[e.From] >= pos[e.To] {
			t.Fatalf("order %v violates edge %s -> %s", p.TopoOrder, e.From, e.To)
		}
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name  string
		graph *Graph
	}{
		{"cuttable without artifact", &Graph{Edges: []Edge{{From: "a", To: "b", Cuttable: true, Cost: 1}}}},
		{"negative cost", &Graph{Edges: []Edge{{From: "a", To: "b", Cuttable: true, Cost: -1, Artifact: "x"}}}},
		{"unauthorized edge with artifact", &Graph{Edges: []Edge{{From: "a", To: "b", Artifact: "old-boot"}}}},
		{"unauthorized edge with cost", &Graph{Edges: []Edge{{From: "a", To: "b", Cost: 3}}}},
		{"duplicate node", &Graph{Nodes: []string{"a", "a"}}},
	}
	for _, tc := range cases {
		if _, err := Solve(tc.graph); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
}

func TestTooManyCuttableEdges(t *testing.T) {
	g := &Graph{}
	for i := 0; i < MaxCuttableEdges+1; i++ {
		g.Edges = append(g.Edges, Edge{From: "a", To: "b", Cuttable: true, Cost: 1, Artifact: "x"})
	}
	if _, err := Solve(g); err == nil {
		t.Fatal("expected error for >25 cuttable edges")
	}
}

// TestAgainstBruteForce cross-checks the branch-and-bound result against an
// exhaustive subset search on random small graphs.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 3 + rng.Intn(4)
		names := make([]string, n)
		for i := range names {
			names[i] = string(rune('a' + i))
		}
		g := &Graph{}
		m := 3 + rng.Intn(6)
		for len(g.Edges) < m {
			e := Edge{From: names[rng.Intn(n)], To: names[rng.Intn(n)]}
			if rng.Intn(2) == 0 {
				e.Cuttable = true
				e.Cost = int64(1 + rng.Intn(9))
				e.Artifact = "boot"
			}
			g.Edges = append(g.Edges, e)
		}
		if err := g.Validate(); err != nil {
			continue
		}
		got, err := Solve(g)
		if err != nil {
			t.Fatal(err)
		}
		wantFeasible, wantCost := bruteForce(g)
		if got.Feasible != wantFeasible {
			t.Fatalf("trial %d: feasible=%v want %v (graph %+v)", trial, got.Feasible, wantFeasible, g.Edges)
		}
		if got.Feasible && got.TotalCost != wantCost {
			t.Fatalf("trial %d: cost=%d want %d (graph %+v)", trial, got.TotalCost, wantCost, g.Edges)
		}
		if got.Feasible {
			assertValidPlan(t, g, got)
		}
	}
}

func bruteForce(g *Graph) (feasible bool, cost int64) {
	var cuttable []int
	for i, e := range g.Edges {
		if e.Cuttable {
			cuttable = append(cuttable, i)
		}
	}
	_, idx := nodeIndex(g)
	best := int64(-1)
	for mask := 0; mask < 1<<uint(len(cuttable)); mask++ {
		active := make([]bool, len(g.Edges))
		var c int64
		for i := range g.Edges {
			active[i] = true
		}
		for j, ei := range cuttable {
			if mask&(1<<uint(j)) != 0 {
				active[ei] = false
				c += g.Edges[ei].Cost
			}
		}
		if findCycle(g, idx, active) == nil && (best < 0 || c < best) {
			best = c
		}
	}
	if best < 0 {
		return false, 0
	}
	return true, best
}

func assertValidPlan(t *testing.T, g *Graph, p *Plan) {
	t.Helper()
	cuts := cutSet(p)
	// Every cut edge must be authorized and carry its own designated artifact.
	for _, c := range p.Cuts {
		e := g.Edges[c.EdgeID]
		if !e.Cuttable {
			t.Fatalf("cut unauthorized edge %d", c.EdgeID)
		}
		if c.Artifact != e.Artifact {
			t.Fatalf("edge %d cut with artifact %q, want designated %q", c.EdgeID, c.Artifact, e.Artifact)
		}
	}
	// Remaining graph must be acyclic and topo order must respect it.
	_, idx := nodeIndex(g)
	active := make([]bool, len(g.Edges))
	for i := range g.Edges {
		active[i] = !cuts[i]
	}
	if cyc := findCycle(g, idx, active); cyc != nil {
		t.Fatalf("remaining graph still has cycle over edges %v", cyc)
	}
	pos := make(map[string]int)
	for i, n := range p.TopoOrder {
		pos[n] = i
	}
	if len(pos) != len(idx) {
		t.Fatalf("topo order covers %d nodes, want %d", len(pos), len(idx))
	}
	for i, e := range g.Edges {
		if cuts[i] {
			continue
		}
		if pos[e.From] >= pos[e.To] {
			t.Fatalf("topo order violates remaining edge %s -> %s", e.From, e.To)
		}
	}
	// Reported cost must match the cuts.
	var sum int64
	for _, c := range p.Cuts {
		sum += c.Cost
	}
	if sum != p.TotalCost {
		t.Fatalf("total cost %d != sum of cuts %d", p.TotalCost, sum)
	}
}
