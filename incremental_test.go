package coreindex_test

import (
	"reflect"
	"testing"

	"coreindex"
)

// TestCascadeDeleteInternalEdge: deleting one internal edge of a
// 5-clique cascades: every clique node drops from core 4 to core 3.
func TestCascadeDeleteInternalEdge(t *testing.T) {
	var ops []coreindex.EdgeOp
	ops = addClique(ops, 1, 2, 3, 4, 5)
	ix := buildGraph(t, nil, ops)
	for id := int64(1); id <= 5; id++ {
		if c, _ := ix.Core(id); c != 4 {
			t.Fatalf("node %d: core %d, want 4", id, c)
		}
	}

	changed, err := ix.DeleteEdge(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]int{1: 3, 2: 3, 3: 3, 4: 3, 5: 3}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
	if got := coresOf(ix); !reflect.DeepEqual(got, want) {
		t.Fatalf("cores = %v, want %v", got, want)
	}

	// K4 variant: deleting one internal edge drops all four 3 -> 2.
	var ops4 []coreindex.EdgeOp
	ops4 = addClique(ops4, 1, 2, 3, 4)
	ix4 := buildGraph(t, nil, ops4)
	changed, err = ix4.DeleteEdge(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want4 := map[int64]int{1: 2, 2: 2, 3: 2, 4: 2}
	if !reflect.DeepEqual(changed, want4) {
		t.Fatalf("K4 cascade: changed = %v, want %v", changed, want4)
	}
}

// TestBridgeScenario: two high-core cliques joined by a low-core bridge;
// updates to the bridge and to clique-internal edges affect exactly the
// right node sets.
func TestBridgeScenario(t *testing.T) {
	var ops []coreindex.EdgeOp
	ops = addClique(ops, 1, 2, 3, 4, 5)
	ops = addClique(ops, 6, 7, 8, 9, 10)
	ops = append(ops, coreindex.AddOp(5, 11), coreindex.AddOp(11, 6))
	ix := buildGraph(t, nil, ops)

	// Delete one bridge edge: only the bridge node drops 2 -> 1.
	changed, err := ix.DeleteEdge(5, 11)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]int{11: 1}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("bridge cut: changed = %v, want %v", changed, want)
	}
	// Delete the other bridge edge: node 11 becomes isolated (core 0).
	changed, err = ix.DeleteEdge(11, 6)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]int{11: 0}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("bridge cut 2: changed = %v, want %v", changed, want)
	}
	// Reconnect the bridge: node 11 climbs back 0 -> 1 -> 2.
	changed, err = ix.ApplyBatch([]coreindex.EdgeOp{coreindex.AddOp(5, 11), coreindex.AddOp(11, 6)})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]int{11: 2}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("bridge restore: changed = %v, want %v", changed, want)
	}
	// Delete a clique-internal edge: the whole first clique cascades
	// 4 -> 3, the second clique and the bridge are untouched.
	changed, err = ix.DeleteEdge(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]int{1: 3, 2: 3, 3: 3, 4: 3, 5: 3}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("internal edge cascade: changed = %v, want %v", changed, want)
	}
	for id := int64(6); id <= 10; id++ {
		if c, _ := ix.Core(id); c != 4 {
			t.Fatalf("node %d: core %d, want 4", id, c)
		}
	}
	if c, _ := ix.Core(11); c != 2 {
		t.Fatalf("node 11: core %d, want 2", c)
	}
}

// TestIdempotentAndNoop: duplicate inserts, deletes of missing edges,
// and add+delete pairs inside one batch leave the state unchanged.
func TestIdempotentAndNoop(t *testing.T) {
	ix := buildGraph(t, nil, []coreindex.EdgeOp{coreindex.AddOp(1, 2), coreindex.AddOp(2, 3)})
	before := coresOf(ix)
	beforeEdges := ix.Edges()

	// Duplicate inserts (both directions) and deletes of missing edges.
	changed, err := ix.ApplyBatch([]coreindex.EdgeOp{
		coreindex.AddOp(1, 2), coreindex.AddOp(2, 1), // already present
		coreindex.DelOp(1, 3), coreindex.DelOp(9, 9-1), // missing
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Fatalf("no-op batch changed nodes: %v", changed)
	}
	// Add and remove the same new edge within one batch: net zero.
	changed, err = ix.ApplyBatch([]coreindex.EdgeOp{
		coreindex.AddOp(1, 3), coreindex.DelOp(3, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Fatalf("add+delete batch changed nodes: %v", changed)
	}
	if got := coresOf(ix); !reflect.DeepEqual(got, before) {
		t.Fatalf("cores drifted: %v, want %v", got, before)
	}
	if got := ix.Edges(); !reflect.DeepEqual(got, beforeEdges) {
		t.Fatalf("edges drifted: %v, want %v", got, beforeEdges)
	}
}

// TestBatchRollback: a batch containing a self-loop fails and every
// earlier op in the batch is rolled back.
func TestBatchRollback(t *testing.T) {
	ix := buildGraph(t, []int64{7}, []coreindex.EdgeOp{coreindex.AddOp(1, 2)})
	before := coresOf(ix)
	beforeEdges := ix.Edges()

	_, err := ix.ApplyBatch([]coreindex.EdgeOp{
		coreindex.AddOp(3, 4), // valid, must be rolled back
		coreindex.AddOp(4, 5), // valid, must be rolled back
		coreindex.AddOp(5, 5), // self-loop: batch fails
	}, 8) // isolated node, must be rolled back too
	if err != coreindex.ErrSelfLoop {
		t.Fatalf("err = %v, want ErrSelfLoop", err)
	}
	if got := coresOf(ix); !reflect.DeepEqual(got, before) {
		t.Fatalf("cores after rollback = %v, want %v", got, before)
	}
	if got := ix.Edges(); !reflect.DeepEqual(got, beforeEdges) {
		t.Fatalf("edges after rollback = %v, want %v", got, beforeEdges)
	}
	for _, id := range []int64{3, 4, 5, 8} {
		if ix.HasNode(id) {
			t.Fatalf("node %d survived rollback", id)
		}
	}
	// Single self-loop op is rejected as well.
	if _, err := ix.AddEdge(1, 1); err != coreindex.ErrSelfLoop {
		t.Fatalf("AddEdge self-loop: err = %v", err)
	}
}

// TestRepeatedOperations: applying the same op sequence repeatedly is
// stable, and cores always match a full recomputation.
func TestRepeatedOperations(t *testing.T) {
	ix := coreindex.New()
	seq := []coreindex.EdgeOp{
		coreindex.AddOp(1, 2), coreindex.AddOp(2, 3), coreindex.AddOp(3, 1),
		coreindex.DelOp(1, 2), coreindex.DelOp(1, 2), // repeated delete
		coreindex.AddOp(1, 2), coreindex.AddOp(1, 2), // repeated insert
	}
	for round := 0; round < 5; round++ {
		if _, err := ix.ApplyBatch(seq); err != nil {
			t.Fatal(err)
		}
		ref := ix.Decompose().Core
		if got := coresOf(ix); !reflect.DeepEqual(got, ref) {
			t.Fatalf("round %d: cores %v, reference %v", round, got, ref)
		}
	}
	// Final graph: triangle 1-2-3, all core 2.
	want := map[int64]int{1: 2, 2: 2, 3: 2}
	if got := coresOf(ix); !reflect.DeepEqual(got, want) {
		t.Fatalf("cores = %v, want %v", got, want)
	}
}
