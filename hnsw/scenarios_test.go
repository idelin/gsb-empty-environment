package hnsw

import "testing"

// TestHeuristicDiversity verifies Algorithm 4 keeps a distant node from
// another cluster instead of filling all slots with tightly clustered near
// points, and that keepPrunedConnections backfills skipped candidates.
func TestHeuristicDiversity(t *testing.T) {
	g := newTestGraph(t, 2, false)
	// Target at the origin. Three tight near points, one clearly farther node
	// in the opposite direction.
	addNode(t, g, 0, vec2{0, 0}, 0)
	addNode(t, g, 1, vec2{1, 0}, 0)
	addNode(t, g, 2, vec2{1.02, 0.05}, 0)
	addNode(t, g, 3, vec2{1.04, -0.04}, 0)
	addNode(t, g, 4, vec2{-4, 0}, 0)

	candidates := map[int]struct{}{1: {}, 2: {}, 3: {}, 4: {}}
	got := g.selectNeighbors(0, candidates, 0, 2)
	if len(got) != 2 {
		t.Fatalf("heuristic returned %d neighbors, want 2: %v", len(got), got)
	}
	if !containsID(got, 4) {
		t.Fatalf("heuristic dropped far-cluster node 4, got %v", got)
	}
	nearCount := 0
	for _, id := range got {
		if id != 4 {
			nearCount++
		}
	}
	if nearCount != 1 {
		t.Fatalf("expected exactly one near-cluster node, got %v", got)
	}

	// keepPrunedConnections backfills with skipped candidates while still
	// respecting the degree bound and keeping the far node.
	g.keepPruned = true
	backfilled := g.selectNeighbors(0, candidates, 0, 2)
	if len(backfilled) != 2 || !containsID(backfilled, 4) {
		t.Fatalf("keepPruned backfill mismatch, got %v", backfilled)
	}
}

// TestDeleteBridgeHub builds two clusters connected solely through a hub and
// checks that deleting the hub re-bridges the clusters through its neighbors.
func TestDeleteBridgeHub(t *testing.T) {
	g := newTestGraph(t, 2, false)
	// Left cluster {1,2}, hub 0, right cluster {3,4}. Every cross-cluster
	// path goes through the hub on layer 0.
	addNode(t, g, 0, vec2{0, 0}, 0)
	addNode(t, g, 1, vec2{-1, 0}, 0)
	addNode(t, g, 2, vec2{-1.1, 0.2}, 0)
	addNode(t, g, 3, vec2{1, 0}, 0)
	addNode(t, g, 4, vec2{1.1, -0.2}, 0)
	addEdge(t, g, 0, 0, 1)
	addEdge(t, g, 0, 0, 2)
	addEdge(t, g, 0, 0, 3)
	addEdge(t, g, 0, 0, 4)
	addEdge(t, g, 0, 1, 2)
	addEdge(t, g, 0, 3, 4)
	assertInvariants(t, g, "setup")
	if !g.Layer0Connected() {
		t.Fatal("setup graph is not connected")
	}

	report, err := g.Delete([]int{0})
	if err != nil {
		t.Fatalf("Delete hub: %v", err)
	}
	assertInvariants(t, g, "hub deletion")
	if !g.Layer0Connected() {
		t.Fatal("layer 0 fragmented after hub deletion; re-bridging failed")
	}
	if len(report.Deleted) != 1 || report.Deleted[0] != 0 {
		t.Fatalf("unexpected delete report: %+v", report)
	}
	if len(report.Repaired) != 4 {
		t.Fatalf("expected 4 repaired neighbors, got %v", report.Repaired)
	}

	neighbors, err := g.Neighbors(1, 0)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	boundary := false
	for _, id := range neighbors {
		if id == 3 || id == 4 {
			boundary = true
		}
	}
	if !boundary {
		t.Fatalf("left cluster node 1 has no cross-cluster edge: %v", neighbors)
	}

	hits, err := g.KNNFrom(1, 4, 16)
	if err != nil {
		t.Fatalf("KNNFrom: %v", err)
	}
	// Four alive nodes remain; KNNFrom excludes node 1 itself, leaving three
	// comparable hits.
	wantAll := bruteForce(g, g.nodes[1].vec, 4)
	want := wantAll[:0]
	for _, hit := range wantAll {
		if hit.ID != 1 {
			want = append(want, hit)
		}
	}
	if len(hits) != len(want) {
		t.Fatalf("KNN length = %d, want %d", len(hits), len(want))
	}
	for i := range want {
		if hits[i].ID != want[i].ID {
			t.Fatalf("KNN ids = %v, want %v", resultIDs(hits), resultIDs(want))
		}
	}
}

// TestDeleteEntryMigration deletes the top-layer entry node and follows entry
// migration down to full graph depletion.
func TestDeleteEntryMigration(t *testing.T) {
	g := newTestGraph(t, 2, false)
	addNode(t, g, 1, vec2{0, 0}, 2) // highest node, initial entry
	addNode(t, g, 2, vec2{1, 0}, 1)
	addNode(t, g, 3, vec2{2, 0}, 0)
	addNode(t, g, 4, vec2{3, 0}, 0)
	addEdge(t, g, 1, 1, 2)
	addEdge(t, g, 0, 2, 3)
	addEdge(t, g, 0, 3, 4)
	addEdge(t, g, 0, 2, 4)

	if entry, ok := g.Entry(); !ok || entry != 1 {
		t.Fatalf("initial entry = %d (%v)", entry, ok)
	}

	if _, err := g.Delete([]int{1}); err != nil {
		t.Fatalf("Delete entry: %v", err)
	}
	assertInvariants(t, g, "top entry deletion")
	if entry, ok := g.Entry(); !ok || entry != 2 {
		t.Fatalf("entry after migration = %d (%v), want 2", entry, ok)
	}

	if _, err := g.Delete([]int{2}); err != nil {
		t.Fatalf("Delete second entry: %v", err)
	}
	assertInvariants(t, g, "second entry deletion")
	if entry, ok := g.Entry(); !ok || entry != 3 {
		t.Fatalf("entry after second migration = %d (%v), want 3", entry, ok)
	}

	if _, err := g.Delete([]int{3, 4}); err != nil {
		t.Fatalf("Delete all: %v", err)
	}
	assertInvariants(t, g, "full deletion")
	if _, ok := g.Entry(); ok {
		t.Fatal("entry must be absent after full deletion")
	}
	if g.Alive() != 0 || len(g.AliveIDs()) != 0 {
		t.Fatal("expected no alive nodes")
	}

	again, err := g.Delete([]int{3, 4})
	if err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
	if len(again.Skipped) != 2 || len(again.Deleted) != 0 {
		t.Fatalf("unexpected re-delete report: %+v", again)
	}
	assertInvariants(t, g, "re-delete")
}

// TestEmptyAndFullBoundaries covers the empty graph, unknown deletions, and
// searching an exhausted graph.
func TestEmptyAndFullBoundaries(t *testing.T) {
	g := newTestGraph(t, 2, false)
	if _, ok := g.Entry(); ok {
		t.Fatal("fresh graph should have no entry")
	}
	if !g.Layer0Connected() {
		t.Fatal("empty graph should be connected vacuously")
	}
	hits, err := g.KNN(vec2{0, 0}, 3, 8)
	if err != nil {
		t.Fatalf("KNN on empty graph: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("empty KNN returned %v", hits)
	}
	if _, err := g.Delete(nil); err != nil {
		t.Fatalf("empty Delete: %v", err)
	}
	if _, err := g.Delete([]int{99}); err == nil {
		t.Fatal("deleting an unknown node must fail")
	}

	addNode(t, g, 7, vec2{1, 1}, 0)
	addNode(t, g, 8, vec2{2, 2}, 0)
	addEdge(t, g, 0, 7, 8)
	if _, err := g.Delete([]int{7, 8}); err != nil {
		t.Fatalf("Delete only nodes: %v", err)
	}
	assertInvariants(t, g, "exhausting graph")
	if _, exists := g.IsDeleted(7); !exists {
		t.Fatal("IsDeleted lost node 7")
	}
	if _, err := g.KNNFrom(7, 1, 4); err == nil {
		t.Fatal("KNNFrom on a deleted node must fail")
	}
	if hits, err := g.KNN(vec2{0, 0}, 1, 4); err != nil || len(hits) != 0 {
		t.Fatalf("exhausted KNN = %v, %v", hits, err)
	}
}
