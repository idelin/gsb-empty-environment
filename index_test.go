package bridgeindex

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustApply(t *testing.T, ix *Index, u Update) {
	t.Helper()
	if err := ix.Apply(u); err != nil {
		t.Fatalf("Apply(%+v) failed: %v", u, err)
	}
}

func TestParallelEdgesNotBridges(t *testing.T) {
	ix := New()
	mustApply(t, ix, Update{Add: []Edge{
		{ID: 1, U: 0, V: 1},
		{ID: 2, U: 0, V: 1}, // parallel to edge 1
		{ID: 3, U: 1, V: 2},
	}})

	if ix.IsBridge(1) || ix.IsBridge(2) {
		t.Fatalf("parallel edges 1,2 misjudged as bridges: %v", ix.Bridges())
	}
	info, ok := ix.BridgeInfo(3)
	if !ok {
		t.Fatalf("edge 3 should be a bridge, bridges=%v", ix.Bridges())
	}
	if info.SideA != 2 || info.SideB != 1 {
		t.Fatalf("edge 3 sides = (%d,%d), want (2,1)", info.SideA, info.SideB)
	}

	// Removing one of the parallel pair turns the survivor into a bridge.
	mustApply(t, ix, Update{Remove: []int{2}})
	if !ix.IsBridge(1) {
		t.Fatalf("edge 1 should be a bridge after removing its parallel, bridges=%v", ix.Bridges())
	}
	info, _ = ix.BridgeInfo(1)
	if info.SideA != 1 || info.SideB != 2 {
		t.Fatalf("edge 1 sides = (%d,%d), want (1,2)", info.SideA, info.SideB)
	}
}

func TestSelfLoopRejected(t *testing.T) {
	ix := New()
	mustApply(t, ix, Update{Add: []Edge{{ID: 1, U: 0, V: 1}}})
	if err := ix.Apply(Update{Add: []Edge{{ID: 2, U: 5, V: 5}}}); err == nil {
		t.Fatal("self-loop should be rejected")
	}
	if ix.EdgeCount() != 1 {
		t.Fatalf("edge count = %d, want 1 (state must be unchanged)", ix.EdgeCount())
	}
}

func TestAtomicBatch(t *testing.T) {
	ix := New()
	mustApply(t, ix, Update{Add: []Edge{
		{ID: 1, U: 0, V: 1},
		{ID: 2, U: 1, V: 2},
	}})
	edgesBefore := ix.Edges()
	bridgesBefore := ix.Bridges()

	bad := []Update{
		{Add: []Edge{{ID: 2, U: 2, V: 3}}},                      // duplicate existing ID
		{Remove: []int{99}},                                     // missing edge
		{Add: []Edge{{ID: 7, U: 0, V: 2}, {ID: 7, U: 3, V: 4}}}, // dup within batch
		{Add: []Edge{{ID: 8, U: 0, V: 3}}, Remove: []int{1, 1}}, // dup remove
	}
	for i, u := range bad {
		if err := ix.Apply(u); err == nil {
			t.Fatalf("bad batch %d should fail", i)
		}
		if !reflect.DeepEqual(ix.Edges(), edgesBefore) || !reflect.DeepEqual(ix.Bridges(), bridgesBefore) {
			t.Fatalf("bad batch %d mutated the index", i)
		}
	}

	// A remove+re-add of the same ID in one batch is valid and keeps the ID stable.
	mustApply(t, ix, Update{Add: []Edge{{ID: 2, U: 0, V: 2}}, Remove: []int{2}})
	if ix.EdgeCount() != 2 {
		t.Fatalf("edge count = %d, want 2", ix.EdgeCount())
	}
}

func TestBridgeSideSizesPath(t *testing.T) {
	ix := New()
	mustApply(t, ix, Update{Add: []Edge{
		{ID: 10, U: 0, V: 1},
		{ID: 11, U: 1, V: 2},
		{ID: 12, U: 2, V: 3},
	}})
	want := []BridgeInfo{
		{EdgeID: 10, SideA: 1, SideB: 3},
		{EdgeID: 11, SideA: 2, SideB: 2},
		{EdgeID: 12, SideA: 3, SideB: 1},
	}
	if got := ix.Bridges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("bridges = %v, want %v", got, want)
	}
}

func TestDisconnectedGraph(t *testing.T) {
	ix := New()
	mustApply(t, ix, Update{Add: []Edge{
		{ID: 1, U: 0, V: 1},
		{ID: 2, U: 1, V: 2},
		{ID: 3, U: 0, V: 2}, // triangle: no bridges here
		{ID: 4, U: 7, V: 8}, // separate component: bridge, sides 1 and 1
	}})
	got := ix.Bridges()
	want := []BridgeInfo{{EdgeID: 4, SideA: 1, SideB: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bridges = %v, want %v", got, want)
	}
	if n, err := ReferenceCheck(ix); err != nil || n != 4 {
		t.Fatalf("ReferenceCheck = (%d, %v), want (4, nil)", n, err)
	}
}

func TestRandomizedAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	ix := New()
	nextID := 1
	present := map[int]bool{}
	const batches = 400
	totalChecked := 0

	for batch := 0; batch < batches; batch++ {
		var u Update
		for id := range present {
			if rng.Intn(4) == 0 {
				u.Remove = append(u.Remove, id)
			}
		}
		for _, id := range u.Remove {
			delete(present, id)
		}
		for i, n := 0, rng.Intn(6); i < n; i++ {
			a, b := rng.Intn(9), rng.Intn(9)
			if a == b {
				continue // self-loops are not allowed
			}
			e := Edge{ID: nextID, U: a, V: b}
			nextID++
			u.Add = append(u.Add, e)
			present[e.ID] = true
		}
		if err := ix.Apply(u); err != nil {
			t.Fatalf("batch %d: Apply failed: %v", batch, err)
		}
		checked, err := ReferenceCheck(ix)
		totalChecked += checked
		if err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
	}
	t.Logf("reference-checked %d edge deletions across %d atomic batches (final graph: %d edges, %d nodes, %d bridges)",
		totalChecked, batches, ix.EdgeCount(), ix.NodeCount(), len(ix.Bridges()))
	if totalChecked == 0 {
		t.Fatal("reference checker never ran")
	}
}
