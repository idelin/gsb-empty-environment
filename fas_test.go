package fas

import (
	"math/rand"
	"testing"
)

// sampleGraph holds three overlapping directed cycles that all share the
// key edge 0->1 (w=5); every other edge weighs 6:
//
//	X: 0->1->2->0
//	Y: 0->1->3->0
//	Z: 0->1->2->4->3->0
//
// The unique minimum-weight feedback arc set is {0->1} with weight 5:
// without it, at least two weight-6 edges are needed (e.g. 1->2 and 3->0).
func sampleGraph() *Graph {
	g := New(5)
	g.Add(0, 1, 5)
	g.Add(1, 2, 6)
	g.Add(2, 0, 6)
	g.Add(1, 3, 6)
	g.Add(3, 0, 6)
	g.Add(2, 4, 6)
	g.Add(4, 3, 6)
	return g
}

func hasEdge(es []Edge, from, to int) bool {
	for _, e := range es {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestSampleGraphHasOverlappingCycles(t *testing.T) {
	g := sampleGraph()
	if IsDAG(g) {
		t.Fatal("sample graph should be cyclic")
	}
	// Removing any single non-key edge must leave at least one of the
	// overlapping cycles intact, proving the cycles overlap on 0->1.
	for i, e := range g.Edges {
		if e.From == 0 && e.To == 1 {
			continue
		}
		rest := Subtract(g, []Edge{g.Edges[i]})
		if IsDAG(rest) {
			t.Errorf("removing only non-key edge %d->%d should not break all cycles", e.From, e.To)
		}
	}
	// Removing the shared key edge alone breaks every cycle.
	if !IsDAG(Subtract(g, []Edge{{From: 0, To: 1, Weight: 5}})) {
		t.Error("removing the shared key edge 0->1 should break all cycles")
	}
}

func TestGreedySharedCutsKeyEdge(t *testing.T) {
	rem := GreedyShared(sampleGraph())
	if len(rem) != 1 || rem[0].From != 0 || rem[0].To != 1 {
		t.Fatalf("greedy-shared should cut exactly the shared key edge 0->1, got %v", rem)
	}
	if w := TotalWeight(rem); w != 5 {
		t.Errorf("total removed weight = %v, want 5", w)
	}
}

func TestGreedyMinWeightCutsKeyEdge(t *testing.T) {
	rem := GreedyMinWeight(sampleGraph())
	if len(rem) != 1 || rem[0].From != 0 || rem[0].To != 1 {
		t.Fatalf("greedy-min-weight should cut exactly 0->1, got %v", rem)
	}
}

func TestHybridPicksLightestResult(t *testing.T) {
	res := Solve(sampleGraph())
	if res.TotalWeight != 5 {
		t.Errorf("hybrid total weight = %v, want 5 (heuristic %s)", res.TotalWeight, res.Heuristic)
	}
	if len(res.Removed) != 1 || !hasEdge(res.Removed, 0, 1) {
		t.Errorf("hybrid removed set = %v, want exactly [0->1]", res.Removed)
	}
}

// TestHybridBeatsIndividualHeuristics uses two fixtures where individual
// heuristics are provably suboptimal (weights verified by brute force);
// the hybrid solver must still return the optimum for each.
func TestHybridBeatsIndividualHeuristics(t *testing.T) {
	// Single cycle 1->2->3->1: the shared-edge greedy ties on coverage
	// and cuts the heaviest edge 3->1 (9) instead of a weight-1 edge.
	g1 := New(5)
	g1.Add(1, 0, 6)
	g1.Add(1, 2, 1)
	g1.Add(2, 3, 1)
	g1.Add(2, 4, 6)
	g1.Add(3, 1, 9)
	g1.Add(3, 4, 9)
	g1.Add(4, 0, 5)
	if w := TotalWeight(GreedyShared(g1)); w != 9 {
		t.Errorf("greedy-shared weight = %v, want 9", w)
	}
	if res := Solve(g1); res.TotalWeight != 1 {
		t.Errorf("hybrid weight = %v, want 1 (heuristic %s)", res.TotalWeight, res.Heuristic)
	}

	// Single cycle 0->1->3->0: the topological ordering makes the
	// weight-4 edge backward instead of cutting the cheapest edge (3).
	g2 := New(4)
	g2.Add(0, 1, 5)
	g2.Add(1, 3, 3)
	g2.Add(3, 0, 4)
	if w := TotalWeight(Topological(g2)); w != 4 {
		t.Errorf("topological weight = %v, want 4", w)
	}
	res := Solve(g2)
	if res.TotalWeight != 3 {
		t.Errorf("hybrid weight = %v, want 3 (heuristic %s)", res.TotalWeight, res.Heuristic)
	}
	if !IsDAG(Subtract(g2, res.Removed)) {
		t.Error("remaining graph must be acyclic")
	}
}

func TestRemainingGraphIsStrictlyAcyclic(t *testing.T) {
	g := sampleGraph()
	res := Solve(g)
	rest := Subtract(g, res.Removed)
	if !IsDAG(rest) {
		t.Fatal("remaining graph must be strictly acyclic")
	}
	if !VerifyOrder(rest, res.TopoOrder) {
		t.Fatalf("topological order %v does not certify the remaining graph", res.TopoOrder)
	}
	// Every remaining edge must go forward in the returned order.
	pos := make([]int, rest.NumNodes)
	for i, v := range res.TopoOrder {
		pos[v] = i
	}
	for _, e := range rest.Edges {
		if pos[e.From] >= pos[e.To] {
			t.Errorf("edge %d->%d violates topological order %v", e.From, e.To, res.TopoOrder)
		}
	}
}

func TestDAGInputNeedsNoRemoval(t *testing.T) {
	g := New(4)
	g.Add(0, 1, 1)
	g.Add(1, 2, 1)
	g.Add(0, 3, 1)
	g.Add(3, 2, 1)
	res := Solve(g)
	if len(res.Removed) != 0 || res.TotalWeight != 0 {
		t.Errorf("DAG input should yield an empty FAS, got %v", res.Removed)
	}
	if !VerifyOrder(g, res.TopoOrder) {
		t.Errorf("order %v invalid for DAG input", res.TopoOrder)
	}
}

func TestSelfLoopIsRemoved(t *testing.T) {
	g := New(2)
	g.Add(0, 0, 3)
	g.Add(0, 1, 1)
	for name, fn := range map[string]func(*Graph) []Edge{
		"greedy-shared":     GreedyShared,
		"greedy-min-weight": GreedyMinWeight,
		"topological":       Topological,
	} {
		rem := fn(g)
		if !hasEdge(rem, 0, 0) {
			t.Errorf("%s must remove the self-loop, got %v", name, rem)
		}
		if !IsDAG(Subtract(g, rem)) {
			t.Errorf("%s left a cyclic graph", name)
		}
	}
}

func TestDisconnectedCycles(t *testing.T) {
	g := New(6)
	g.Add(0, 1, 2)
	g.Add(1, 0, 4) // cycle A, cheapest cut 2
	g.Add(2, 3, 1)
	g.Add(3, 4, 1)
	g.Add(4, 2, 1) // cycle B, cheapest cut 1
	res := Solve(g)
	if res.TotalWeight != 3 {
		t.Errorf("total weight = %v, want 3", res.TotalWeight)
	}
	if !IsDAG(Subtract(g, res.Removed)) {
		t.Error("remaining graph must be acyclic")
	}
}

func TestVerifyOrderRejectsBadOrder(t *testing.T) {
	g := New(3)
	g.Add(0, 1, 1)
	g.Add(1, 2, 1)
	if VerifyOrder(g, []int{2, 1, 0}) {
		t.Error("reversed order must be rejected")
	}
	if VerifyOrder(g, []int{0, 1, 1}) {
		t.Error("non-permutation must be rejected")
	}
	if VerifyOrder(g, []int{0, 1}) {
		t.Error("short order must be rejected")
	}
}

// TestRandomGraphsAlwaysAcyclicRemainder is a property test: on random
// digraphs every heuristic must leave a DAG, and Solve must return a
// valid topological certificate with weight no larger than any heuristic.
func TestRandomGraphsAlwaysAcyclicRemainder(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 2 + rng.Intn(8)
		g := New(n)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if rng.Float64() < 0.3 {
					g.Add(i, j, 1+float64(rng.Intn(9)))
				}
			}
		}
		for name, fn := range map[string]func(*Graph) []Edge{
			"greedy-shared":     GreedyShared,
			"greedy-min-weight": GreedyMinWeight,
			"topological":       Topological,
		} {
			rem := fn(g)
			if !IsDAG(Subtract(g, rem)) {
				t.Fatalf("trial %d: %s left a cyclic graph", trial, name)
			}
		}
		res := Solve(g)
		rest := Subtract(g, res.Removed)
		if !VerifyOrder(rest, res.TopoOrder) {
			t.Fatalf("trial %d: invalid topological certificate %v", trial, res.TopoOrder)
		}
		for _, fn := range []func(*Graph) []Edge{GreedyShared, GreedyMinWeight, Topological} {
			if w := TotalWeight(fn(g)); res.TotalWeight > w {
				t.Fatalf("trial %d: hybrid weight %v exceeds heuristic weight %v", trial, res.TotalWeight, w)
			}
		}
	}
}
