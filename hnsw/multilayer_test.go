package hnsw

import "testing"

// TestUpperLayerRepair verifies that deleting a multi-layer hub repairs
// every layer it spans, preserves the tighter M bound above layer 0, and
// leaves the entry point on the highest surviving layer.
func TestUpperLayerRepair(t *testing.T) {
	g := newTestGraph(t, 2, false)
	// Node 0 spans layers 0..2; nodes 1..3 span 0..1; 4..5 are layer 0.
	addNode(t, g, 0, vec2{0, 0}, 2)
	addNode(t, g, 1, vec2{1, 0}, 1)
	addNode(t, g, 2, vec2{-1, 0}, 1)
	addNode(t, g, 3, vec2{0, 1}, 1)
	addNode(t, g, 4, vec2{2, 0}, 0)
	addNode(t, g, 5, vec2{-2, 0}, 0)

	// Layer 2: hub alone. Layer 1: hub connected to all layer-1 peers.
	for _, id := range []int{1, 2, 3} {
		addEdge(t, g, 1, 0, id)
	}
	// Layer 0: hub spokes plus a base chain.
	for _, id := range []int{1, 2, 3, 4, 5} {
		addEdge(t, g, 0, 0, id)
	}
	addEdge(t, g, 0, 1, 4)
	addEdge(t, g, 0, 2, 5)

	if _, err := g.Delete([]int{0}); err != nil {
		t.Fatalf("Delete multi-layer hub: %v", err)
	}
	if err := g.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}

	entry, ok := g.Entry()
	if !ok || entry != 1 {
		t.Fatalf("entry = %d (%v), want smallest top-layer node 1", entry, ok)
	}
	for id, ns := range g.nodes {
		if ns.deleted {
			continue
		}
		for layer := 0; layer <= ns.level; layer++ {
			if len(ns.conns[layer]) > g.MaxDegree(layer) {
				t.Fatalf("node %d layer %d exceeds bound %d", id, layer, g.MaxDegree(layer))
			}
			for _, neighborID := range ns.conns[layer] {
				neighbor := g.nodes[neighborID]
				if neighbor.deleted {
					t.Fatalf("dangling edge %d->%d layer %d", id, neighborID, layer)
				}
				if !containsID(neighbor.conns[layer], id) {
					t.Fatalf("asymmetric edge %d-%d layer %d", id, neighborID, layer)
				}
			}
		}
	}

	if !g.Layer0Connected() {
		t.Fatal("layer 0 disconnected after multi-layer hub deletion")
	}

	// Upper-layer surviving peers must retain at least one layer-1 link so
	// greedy descent still has a route.
	upperLinks := 0
	for _, id := range []int{1, 2, 3} {
		upperLinks += len(g.nodes[id].conns[1])
	}
	if upperLinks == 0 {
		t.Fatal("all layer-1 links vanished with the hub")
	}

	hits, err := g.KNN(vec2{0.2, 0.1}, 4, 16)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	want := bruteForce(g, vec2{0.2, 0.1}, 4)
	if len(hits) != len(want) {
		t.Fatalf("KNN length = %d, want %d", len(hits), len(want))
	}
	for i := range want {
		if hits[i].ID != want[i].ID {
			t.Fatalf("KNN ids = %v, want %v", resultIDs(hits), resultIDs(want))
		}
	}
}

// TestKNNFromSmallGraph checks result trimming when fewer than k alive
// neighbors exist.
func TestKNNFromSmallGraph(t *testing.T) {
	g := newTestGraph(t, 2, false)
	addNode(t, g, 0, vec2{0, 0}, 0)
	addNode(t, g, 1, vec2{1, 0}, 0)
	addEdge(t, g, 0, 0, 1)

	hits, err := g.KNNFrom(0, 10, 16)
	if err != nil {
		t.Fatalf("KNNFrom: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != 1 {
		t.Fatalf("KNNFrom = %v, want only node 1", hits)
	}
}
