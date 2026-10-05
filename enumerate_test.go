package main

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustSolve(t *testing.T, g *Graph, budget int) *SolveResult {
	t.Helper()
	res, err := solve(g, budget)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return res
}

func TestChain(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 2},
		{From: "a", To: "t", Capacity: 3},
	}}
	res := mustSolve(t, g, -1)
	if res.MaxFlow != 2 {
		t.Fatalf("max flow = %d, want 2", res.MaxFlow)
	}
	want := [][]string{{"s"}}
	if !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", res.Cuts, want)
	}
	if !res.Complete {
		t.Fatal("expected complete enumeration")
	}
}

func TestDiamondLexOrder(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 1},
		{From: "s", To: "b", Capacity: 1},
		{From: "a", To: "t", Capacity: 1},
		{From: "b", To: "t", Capacity: 1},
	}}
	res := mustSolve(t, g, -1)
	if res.MaxFlow != 2 {
		t.Fatalf("max flow = %d, want 2", res.MaxFlow)
	}
	want := [][]string{{"a", "b", "s"}, {"a", "s"}, {"b", "s"}, {"s"}}
	if !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", res.Cuts, want)
	}
}

func TestParallelEdges(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "t", Capacity: 1},
		{From: "s", To: "t", Capacity: 1},
	}}
	res := mustSolve(t, g, -1)
	if res.MaxFlow != 2 {
		t.Fatalf("max flow = %d, want 2", res.MaxFlow)
	}
	if len(res.Cuts) != 1 || len(res.Cuts[0]) != 1 || res.Cuts[0][0] != "s" {
		t.Fatalf("cuts = %v, want [[s]]", res.Cuts)
	}
}

// TestSameCutDistinctResidualPaths uses a graph whose maximum flow admits
// several decompositions (a circulation can ride the a<->b cycle), so the
// same cut shows up through different residual paths. Enumeration must
// still emit each cut exactly once.
func TestSameCutDistinctResidualPaths(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 2},
		{From: "a", To: "b", Capacity: 1},
		{From: "b", To: "a", Capacity: 1},
		{From: "a", To: "t", Capacity: 2},
	}}
	res := mustSolve(t, g, -1)
	want := [][]string{{"a", "b", "s"}, {"s"}}
	if !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", res.Cuts, want)
	}
	seen := map[string]bool{}
	for _, c := range res.Cuts {
		key := ""
		for _, n := range c {
			key += n + ","
		}
		if seen[key] {
			t.Fatalf("duplicate cut emitted: %v", c)
		}
		seen[key] = true
	}
}

func TestIsolatedNodeDoublesCuts(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Nodes: []string{"x"}, Edges: []Edge{
		{From: "s", To: "t", Capacity: 1},
	}}
	res := mustSolve(t, g, -1)
	want := [][]string{{"s"}, {"s", "x"}}
	if !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", res.Cuts, want)
	}
}

func TestZeroFlowDisconnectedSink(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 1},
	}}
	res := mustSolve(t, g, -1)
	if res.MaxFlow != 0 {
		t.Fatalf("max flow = %d, want 0", res.MaxFlow)
	}
	want := [][]string{{"a", "s"}}
	if !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", res.Cuts, want)
	}
}

func TestBudgetTruncation(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{{From: "s", To: "t", Capacity: 1}}}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		g.Nodes = append(g.Nodes, n)
	}
	res := mustSolve(t, g, 100)
	if res.Complete {
		t.Fatal("expected truncated enumeration to be marked incomplete")
	}
	if len(res.Cuts) != 100 {
		t.Fatalf("cuts = %d, want budget 100", len(res.Cuts))
	}
	if err := verifyCutCapacities(g, res.MaxFlow, res.Cuts); err != nil {
		t.Fatalf("per-cut verification: %v", err)
	}
	if err := bruteForceCheck(g, res); err != nil {
		t.Fatalf("brute-force membership check: %v", err)
	}

	full := mustSolve(t, g, -1)
	if !full.Complete || len(full.Cuts) != 1024 {
		t.Fatalf("unlimited run: complete=%v cuts=%d, want complete with 1024", full.Complete, len(full.Cuts))
	}
}

func TestRandomGraphsVsBruteForce(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 2 + r.Intn(7) // 2..8 nodes
		names := make([]string, n)
		for i := range names {
			names[i] = string(rune('a' + i))
		}
		g := &Graph{Source: names[0], Sink: names[n-1]}
		m := r.Intn(n * n)
		for i := 0; i < m; i++ {
			g.Edges = append(g.Edges, Edge{
				From:     names[r.Intn(n)],
				To:       names[r.Intn(n)],
				Capacity: int64(r.Intn(5)),
			})
		}
		res, err := solve(g, -1)
		if err != nil {
			t.Fatalf("seed %d: solve: %v", seed, err)
		}
		if err := verifyCutCapacities(g, res.MaxFlow, res.Cuts); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if err := bruteForceCheck(g, res); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func TestGraphVersionStability(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "t", Capacity: 3},
		{From: "s", To: "a", Capacity: 1},
	}}
	v1 := g.GraphVersion()
	// Edge order must not affect the content hash.
	g2 := &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 1},
		{From: "s", To: "t", Capacity: 3},
	}}
	if v1 != g2.GraphVersion() {
		t.Fatalf("version not canonical: %q vs %q", v1, g2.GraphVersion())
	}
	g3 := &Graph{Version: "release-7", Source: "s", Sink: "t"}
	if g3.GraphVersion() != "release-7" {
		t.Fatalf("explicit version not honored: %q", g3.GraphVersion())
	}
}
