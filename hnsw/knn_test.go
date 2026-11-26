package hnsw

import (
	"math/rand"
	"testing"
)

// TestKNNMatchesBruteForce builds a connected kNN graph, deletes several
// nodes, and checks ef beam search against exact brute-force ranking.
func TestKNNMatchesBruteForce(t *testing.T) {
	const n, k, m = 24, 3, 4
	rng := rand.New(rand.NewSource(42))
	g := newTestGraph(t, m, false)

	coords := make([]vec2, n)
	for id := 0; id < n; id++ {
		coords[id] = vec2{x: rng.Float64() * 10, y: rng.Float64() * 10}
		addNode(t, g, id, coords[id], 0)
	}
	// Symmetric kNN ground graph keeps layer 0 connected and under M0=8.
	for id := 0; id < n; id++ {
		for _, neighborID := range nearestIDs(coords, id, k) {
			addEdge(t, g, 0, id, neighborID)
		}
	}
	assertInvariants(t, g, "kNN build")
	if !g.Layer0Connected() {
		t.Fatal("kNN ground graph unexpectedly disconnected")
	}

	deleted := []int{2, 5, 9, 14, 20}
	if _, err := g.Delete(deleted); err != nil {
		t.Fatalf("Delete batch: %v", err)
	}
	assertInvariants(t, g, "random batch deletion")
	if !g.Layer0Connected() {
		t.Fatal("layer 0 fragmented after batch deletion")
	}

	queries := []vec2{
		{0, 0},
		{5, 5},
		{10, 10},
		{-1, 4},
		{7, 2},
	}
	for _, q := range queries {
		got, err := g.KNN(q, 5, 20)
		if err != nil {
			t.Fatalf("KNN %+v: %v", q, err)
		}
		want := bruteForce(g, q, 5)
		if len(got) != len(want) {
			t.Fatalf("KNN %+v length = %d, want %d", q, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID || got[i].Distance != want[i].Distance {
				t.Fatalf("KNN %+v = %v, want %v", q, resultIDs(got), resultIDs(want))
			}
		}
	}
}

// TestReverseShrinkHeuristic constructs a hub whose repair forces a surviving
// neighbor above M and verifies the reverse edge is shrunk by the heuristic
// while all edges remain symmetric and degree bounds hold.
func TestReverseShrinkHeuristic(t *testing.T) {
	g := newTestGraph(t, 2, false)
	// Collinear points make Algorithm 4 deterministic. The deleted hub 0
	// initially connects every survivor; repairing node 1 reaches the M=2
	// cap and its added reverse links force symmetric shrinking elsewhere.
	coords := map[int]vec2{
		0: {0, 0},
		1: {1, 0},
		2: {2, 0},
		3: {3, 0},
		4: {4, 0},
		5: {5, 0},
	}
	for id, v := range coords {
		addNode(t, g, id, v, 0)
	}
	for id := 1; id <= 5; id++ {
		addEdge(t, g, 0, 0, id)
	}
	// A small surviving chain so every node has an existing alive neighbor.
	addEdge(t, g, 0, 1, 2)
	addEdge(t, g, 0, 2, 3)
	addEdge(t, g, 0, 3, 4)
	addEdge(t, g, 0, 4, 5)

	report, err := g.Delete([]int{0})
	if err != nil {
		t.Fatalf("Delete hub: %v", err)
	}
	if err := g.CheckInvariants(); err != nil {
		t.Fatalf("invariants after reverse shrink: %v", err)
	}
	if len(report.Repaired) != 5 {
		t.Fatalf("expected 5 repaired nodes, got %v", report.Repaired)
	}
	for _, id := range report.Repaired {
		conns, err := g.Neighbors(id, 0)
		if err != nil {
			t.Fatalf("Neighbors(%d): %v", id, err)
		}
		if len(conns) > g.MaxDegree(0) {
			t.Fatalf("node %d degree %d exceeds M0=%d", id, len(conns), g.MaxDegree(0))
		}
		for _, neighborID := range conns {
			reverse, err := g.Neighbors(neighborID, 0)
			if err != nil {
				t.Fatalf("reverse Neighbors(%d): %v", neighborID, err)
			}
			if !containsID(reverse, id) {
				t.Fatalf("edge %d-%d is not symmetric", id, neighborID)
			}
		}
	}

	// The connected path must survive the shrinking fixpoint.
	if !g.Layer0Connected() {
		t.Fatal("layer 0 disconnected after reverse shrink")
	}
	hits, err := g.KNN(vec2{0.5, 0}, 4, 16)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	want := bruteForce(g, vec2{0.5, 0}, 4)
	if len(hits) != len(want) {
		t.Fatalf("KNN length = %d, want %d", len(hits), len(want))
	}
	for i := range want {
		if hits[i].ID != want[i].ID {
			t.Fatalf("KNN ids = %v, want %v", resultIDs(hits), resultIDs(want))
		}
	}
}

// TestMultiLayerSearchAndDegreeBounds exercises upper-layer greedy routing
// and the tighter M bound on layers above zero after a batch deletion.
func TestMultiLayerSearchAndDegreeBounds(t *testing.T) {
	g := newTestGraph(t, 2, false)
	for id := 0; id < 10; id++ {
		level := 0
		switch {
		case id == 0:
			level = 2
		case id < 4:
			level = 1
		}
		addNode(t, g, id, vec2{float64(id), float64(id % 3)}, level)
	}
	// Upper layer: hub 0 links all layer-1 nodes.
	for id := 1; id < 4; id++ {
		addEdge(t, g, 1, 0, id)
	}
	// Layer 0 chain plus extra hub spokes.
	for id := 1; id < 10; id++ {
		addEdge(t, g, 0, id-1, id)
	}
	for id := 4; id < 10; id++ {
		addEdge(t, g, 0, 0, id)
	}

	if _, err := g.Delete([]int{0}); err != nil {
		t.Fatalf("Delete top hub: %v", err)
	}
	if err := g.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}
	for id, ns := range g.nodes {
		if ns.deleted {
			continue
		}
		for layer := 0; layer <= ns.level; layer++ {
			if len(ns.conns[layer]) > g.MaxDegree(layer) {
				t.Fatalf("node %d layer %d over bound", id, layer)
			}
		}
	}

	hits, err := g.KNN(vec2{8.5, 2.5}, 3, 12)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	want := bruteForce(g, vec2{8.5, 2.5}, 3)
	for i := range want {
		if hits[i].ID != want[i].ID {
			t.Fatalf("multi-layer KNN ids = %v, want %v", resultIDs(hits), resultIDs(want))
		}
	}
}
