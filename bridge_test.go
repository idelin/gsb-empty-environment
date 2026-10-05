package bridgeindex

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustApply(t *testing.T, ix *Index, batch ...Update) []EdgeID {
	t.Helper()
	ids, err := ix.ApplyBatch(batch)
	if err != nil {
		t.Fatalf("ApplyBatch failed: %v", err)
	}
	return ids
}

func bridgeMap(ix *Index) map[EdgeID]BridgeInfo {
	out := make(map[EdgeID]BridgeInfo)
	for _, b := range ix.Bridges() {
		out[b.ID] = b
	}
	return out
}

func TestParallelEdgesAreNotBridges(t *testing.T) {
	ix := New()
	mustApply(t, ix,
		Update{Kind: AddEdge, U: 1, V: 2},
		Update{Kind: AddEdge, U: 1, V: 2}, // parallel edge, same endpoints
	)
	if got := ix.Bridges(); len(got) != 0 {
		t.Fatalf("parallel edges misjudged as bridges: %+v", got)
	}
}

func TestParallelEdgeBecomesBridgeAfterSiblingRemoved(t *testing.T) {
	ix := New()
	ids := mustApply(t, ix,
		Update{Kind: AddEdge, U: 1, V: 2},
		Update{Kind: AddEdge, U: 1, V: 2},
	)
	mustApply(t, ix, Update{Kind: RemoveEdge, ID: ids[0]})
	want := map[EdgeID]BridgeInfo{
		ids[1]: {ID: ids[1], U: 1, V: 2, SideA: 1, SideB: 1},
	}
	if got := bridgeMap(ix); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestBridgeSideCounts(t *testing.T) {
	// Path 1-2-3-4 plus leaf 5 on 2: every edge is a bridge.
	ix := New()
	ids := mustApply(t, ix,
		Update{Kind: AddEdge, U: 1, V: 2},
		Update{Kind: AddEdge, U: 2, V: 3},
		Update{Kind: AddEdge, U: 3, V: 4},
		Update{Kind: AddEdge, U: 2, V: 5},
	)
	want := map[EdgeID]BridgeInfo{
		ids[0]: {ID: ids[0], SideA: 1, SideB: 4},
		ids[1]: {ID: ids[1], SideA: 2, SideB: 3},
		ids[2]: {ID: ids[2], SideA: 1, SideB: 4},
		ids[3]: {ID: ids[3], SideA: 1, SideB: 4},
	}
	got := bridgeMap(ix)
	for id, w := range want {
		g, ok := got[id]
		if !ok || g.SideA != w.SideA || g.SideB != w.SideB {
			t.Fatalf("edge %d: got %+v, want sides (%d,%d)", id, g, w.SideA, w.SideB)
		}
	}
}

func TestAtomicBatchRejection(t *testing.T) {
	ix := New()
	ids := mustApply(t, ix,
		Update{Kind: AddEdge, U: 1, V: 2},
		Update{Kind: AddEdge, U: 2, V: 3},
	)
	before := ix.Bridges()
	// Batch mixes a valid add with an invalid remove: nothing may change.
	_, err := ix.ApplyBatch([]Update{
		{Kind: AddEdge, U: 3, V: 4},
		{Kind: RemoveEdge, ID: EdgeID(999)},
	})
	if err == nil {
		t.Fatal("expected batch to be rejected")
	}
	if ix.EdgeCount() != 2 {
		t.Fatalf("rejected batch mutated edge set, count=%d", ix.EdgeCount())
	}
	if !reflect.DeepEqual(ix.Bridges(), before) {
		t.Fatalf("rejected batch changed bridge set: %+v", ix.Bridges())
	}
	// Double removal inside one batch is also rejected atomically.
	_, err = ix.ApplyBatch([]Update{
		{Kind: RemoveEdge, ID: ids[0]},
		{Kind: RemoveEdge, ID: ids[0]},
	})
	if err == nil || ix.EdgeCount() != 2 {
		t.Fatalf("double-remove batch: err=%v count=%d", err, ix.EdgeCount())
	}
}

func TestSelfLoopRejected(t *testing.T) {
	ix := New()
	if _, err := ix.ApplyBatch([]Update{{Kind: AddEdge, U: 7, V: 7}}); err == nil {
		t.Fatal("self-loop accepted")
	}
	if ix.EdgeCount() != 0 {
		t.Fatal("rejected self-loop mutated the index")
	}
}

func TestEdgeIDsStableAndNeverReused(t *testing.T) {
	ix := New()
	ids1 := mustApply(t, ix, Update{Kind: AddEdge, U: 1, V: 2})
	mustApply(t, ix, Update{Kind: RemoveEdge, ID: ids1[0]})
	ids2 := mustApply(t, ix, Update{Kind: AddEdge, U: 1, V: 2})
	if ids2[0] == ids1[0] {
		t.Fatalf("edge ID %d reused after removal", ids1[0])
	}
	if _, err := ix.ApplyBatch([]Update{{Kind: RemoveEdge, ID: ids1[0]}}); err == nil {
		t.Fatal("stale edge ID accepted")
	}
}

// TestRandomAgainstReference applies random atomic batches to random small
// multigraphs (with parallel edges) and, after every batch, compares the
// index against the delete-each-edge-and-recheck reference. It reports the
// total number of edges actually examined by the reference checker.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	totalChecked := 0
	for trial := 0; trial < 300; trial++ {
		ix := New()
		n := 2 + rng.Intn(7) // vertices 0..n-1
		for step := 0; step < 12; step++ {
			var batch []Update
			removing := make(map[EdgeID]bool)
			for k := 0; k < 1+rng.Intn(4); k++ {
				live := ix.Edges()
				if len(live) > 0 && rng.Intn(3) == 0 {
					id := live[rng.Intn(len(live))].ID
					if removing[id] {
						continue
					}
					removing[id] = true
					batch = append(batch, Update{Kind: RemoveEdge, ID: id})
				} else {
					u, v := rng.Intn(n), rng.Intn(n)
					for v == u {
						v = rng.Intn(n)
					}
					batch = append(batch, Update{Kind: AddEdge, U: u, V: v})
				}
			}
			if _, err := ix.ApplyBatch(batch); err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
			want, checked := ReferenceBridges(ix.Edges())
			totalChecked += checked
			if checked != ix.EdgeCount() {
				t.Fatalf("reference examined %d edges, but %d are live", checked, ix.EdgeCount())
			}
			if got := bridgeMap(ix); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d step %d mismatch\nedges: %+v\ngot:  %+v\nwant: %+v",
					trial, step, ix.Edges(), got, want)
			}
		}
	}
	t.Logf("reference checker actually examined %d edges across all batches", totalChecked)
}
