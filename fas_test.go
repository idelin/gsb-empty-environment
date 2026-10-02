package fas

import "testing"

// overlappingCycles builds the acceptance-test system: three overlapping
// directed cycles sharing the high-weight edge 1->2.
//
//	cycle 1: 0->1->2->0
//	cycle 2: 1->2->3->1   (shares edge 1->2 with cycle 1)
//	cycle 3: 3->4->5->3
func overlappingCycles() *Graph {
	return New(6,
		Edge{From: 0, To: 1, Weight: 2},
		Edge{From: 1, To: 2, Weight: 5}, // shared key edge, highest weight
		Edge{From: 2, To: 0, Weight: 1},
		Edge{From: 2, To: 3, Weight: 1},
		Edge{From: 3, To: 1, Weight: 1},
		Edge{From: 3, To: 4, Weight: 1},
		Edge{From: 4, To: 5, Weight: 1},
		Edge{From: 5, To: 3, Weight: 1},
	)
}

func containsEdge(edges []Edge, target Edge) bool {
	for _, e := range edges {
		if e == target {
			return true
		}
	}
	return false
}

// checkResult verifies the proof obligations of any FAS result:
//  1. removed + remaining reconstruct the original edge multiset;
//  2. the remaining graph is strictly acyclic;
//  3. the emitted topological order is valid for the remaining graph;
//  4. the cut set is minimal: restoring any cut edge recreates a cycle.
func checkResult(t *testing.T, g *Graph, res *Result) {
	t.Helper()
	if got := len(res.Removed) + len(res.Remaining); got != len(g.Edges) {
		t.Fatalf("removed+remaining = %d edges, want %d", got, len(g.Edges))
	}
	for _, e := range res.Removed {
		if !containsEdge(g.Edges, e) {
			t.Fatalf("removed edge %+v not in original graph", e)
		}
	}
	if !IsDAG(g.NumNodes, res.Remaining) {
		t.Fatal("remaining graph is not acyclic")
	}
	if !ValidateTopologicalOrder(g.NumNodes, res.Remaining, res.Order) {
		t.Fatalf("topological order %v is invalid for remaining graph", res.Order)
	}
	for _, e := range res.Removed {
		if IsDAG(g.NumNodes, append(append([]Edge(nil), res.Remaining...), e)) {
			t.Fatalf("cut set not minimal: edge %+v could be restored", e)
		}
	}
}

func TestGreedyCutsSharedHighestWeightEdge(t *testing.T) {
	g := overlappingCycles()
	res := GreedyCycleCut(g)
	key := Edge{From: 1, To: 2, Weight: 5}
	if !containsEdge(res.Removed, key) {
		t.Fatalf("greedy did not cut the shared highest-weight key edge %+v; cut %v", key, res.Removed)
	}
	checkResult(t, g, res)
}

func TestHybridOnOverlappingCycles(t *testing.T) {
	g := overlappingCycles()
	res := Hybrid(g)
	checkResult(t, g, res)
	// Hybrid must be at least as good as pure greedy.
	greedy := GreedyCycleCut(g)
	if res.RemovedWeight() > greedy.RemovedWeight() {
		t.Fatalf("hybrid weight %v worse than greedy %v", res.RemovedWeight(), greedy.RemovedWeight())
	}
	// Optimal for this instance is 4 (cut 0->1, 3->1, 5->3).
	if res.RemovedWeight() != 4 {
		t.Fatalf("hybrid cut weight = %v, want 4", res.RemovedWeight())
	}
}

func TestDAGNeedsNoCut(t *testing.T) {
	g := New(4,
		Edge{From: 0, To: 1, Weight: 1},
		Edge{From: 1, To: 2, Weight: 1},
		Edge{From: 0, To: 3, Weight: 1},
		Edge{From: 3, To: 2, Weight: 1},
	)
	for _, res := range []*Result{GreedyCycleCut(g), Hybrid(g)} {
		if len(res.Removed) != 0 {
			t.Fatalf("%s cut %v from an already-acyclic graph", res.Algorithm, res.Removed)
		}
		checkResult(t, g, res)
	}
}

func TestSelfLoopIsAlwaysCut(t *testing.T) {
	g := New(2,
		Edge{From: 0, To: 0, Weight: 7},
		Edge{From: 0, To: 1, Weight: 1},
	)
	for _, res := range []*Result{GreedyCycleCut(g), Hybrid(g)} {
		if !containsEdge(res.Removed, Edge{From: 0, To: 0, Weight: 7}) {
			t.Fatalf("%s did not cut the self-loop", res.Algorithm)
		}
		checkResult(t, g, res)
	}
}

func TestSingleCycleCutsCheapestAlternative(t *testing.T) {
	// One cycle; hybrid must cut exactly one edge and stay acyclic.
	g := New(3,
		Edge{From: 0, To: 1, Weight: 3},
		Edge{From: 1, To: 2, Weight: 4},
		Edge{From: 2, To: 0, Weight: 2},
	)
	res := Hybrid(g)
	checkResult(t, g, res)
	if len(res.Removed) != 1 {
		t.Fatalf("single cycle needs exactly one cut, got %v", res.Removed)
	}
}

func TestDisconnectedAndEmptyGraph(t *testing.T) {
	g := New(5) // no edges
	res := Hybrid(g)
	if len(res.Removed) != 0 || len(res.Order) != 5 {
		t.Fatalf("empty graph: removed=%v order=%v", res.Removed, res.Order)
	}
	checkResult(t, g, res)
}

func TestDenseOverlappingCycles(t *testing.T) {
	// Complete digraph on 4 nodes (both directions, varied weights):
	// many overlapping cycles; result must still satisfy all proofs.
	var edges []Edge
	w := 1.0
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if i != j {
				edges = append(edges, Edge{From: i, To: j, Weight: w})
				w++
			}
		}
	}
	g := New(4, edges...)
	for _, res := range []*Result{GreedyCycleCut(g), Hybrid(g)} {
		checkResult(t, g, res)
	}
}

func TestTopologicalOrderRespectsAllRemainingEdges(t *testing.T) {
	g := overlappingCycles()
	res := Hybrid(g)
	pos := make([]int, g.NumNodes)
	for i, v := range res.Order {
		pos[v] = i
	}
	for _, e := range res.Remaining {
		if pos[e.From] >= pos[e.To] {
			t.Fatalf("edge %+v violates topological order %v", e, res.Order)
		}
	}
}
