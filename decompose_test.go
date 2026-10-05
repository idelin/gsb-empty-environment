package coreindex_test

import (
	"reflect"
	"testing"

	"coreindex"
)

// addClique appends insertion ops for a clique over nodes.
func addClique(ops []coreindex.EdgeOp, nodes ...int64) []coreindex.EdgeOp {
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			ops = append(ops, coreindex.AddOp(nodes[i], nodes[j]))
		}
	}
	return ops
}

// buildGraph applies ops incrementally from the empty graph and returns
// the index. Initial cores are all zero, so this also exercises the
// incremental insertion path as a construction method.
func buildGraph(t *testing.T, ensure []int64, ops []coreindex.EdgeOp) *coreindex.Index {
	t.Helper()
	ix := coreindex.New()
	if _, err := ix.ApplyBatch(ops, ensure...); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	return ix
}

// coresOf flattens core numbers to id->core for comparison.
func coresOf(ix *coreindex.Index) map[int64]int { return ix.Cores() }

// verifyResidualDegrees checks, for every position i in the peeling
// order, that RemDeg[i] equals the degree of Order[i] in the subgraph
// induced by Order[i:].
func verifyResidualDegrees(t *testing.T, ix *coreindex.Index, d coreindex.Decomposition) {
	t.Helper()
	pos := make(map[int64]int, len(d.Order))
	for i, id := range d.Order {
		pos[id] = i
	}
	if len(pos) != len(d.Order) {
		t.Fatalf("peeling order contains duplicates")
	}
	for i, id := range d.Order {
		deg := 0
		for _, w := range ix.Neighbors(id) {
			if pos[w] >= i {
				deg++
			}
		}
		if deg != d.RemDeg[i] {
			t.Fatalf("node %d: residual degree %d, want %d", id, deg, d.RemDeg[i])
		}
	}
}

// TestTwoCliquesBridge builds two 5-cliques (core 4) linked by a
// low-core bridge path through node 11, and checks the initial build
// output: core values, deterministic peeling order, residual degrees.
func TestTwoCliquesBridge(t *testing.T) {
	var ops []coreindex.EdgeOp
	ops = addClique(ops, 1, 2, 3, 4, 5)
	ops = addClique(ops, 6, 7, 8, 9, 10)
	ops = append(ops, coreindex.AddOp(5, 11), coreindex.AddOp(11, 6))
	ix := buildGraph(t, nil, ops)

	want := map[int64]int{
		1: 4, 2: 4, 3: 4, 4: 4, 5: 4,
		6: 4, 7: 4, 8: 4, 9: 4, 10: 4,
		11: 2, // low-core bridge node
	}
	if got := coresOf(ix); !reflect.DeepEqual(got, want) {
		t.Fatalf("cores = %v, want %v", got, want)
	}

	d := ix.Build()
	if !d.Consistent() {
		t.Fatalf("decomposition not reproducible from order+remdeg")
	}
	verifyResidualDegrees(t, ix, d)

	// Determinism: rebuilding yields the identical peeling order.
	d2 := ix.Decompose()
	if !reflect.DeepEqual(d.Order, d2.Order) || !reflect.DeepEqual(d.RemDeg, d2.RemDeg) {
		t.Fatalf("peeling order not deterministic:\n%v %v\n%v %v", d.Order, d.RemDeg, d2.Order, d2.RemDeg)
	}
	// The bridge node must be peeled before all clique nodes.
	pos := make(map[int64]int)
	for i, id := range d.Order {
		pos[id] = i
	}
	for id := int64(1); id <= 10; id++ {
		if pos[11] > pos[id] {
			t.Fatalf("bridge node 11 (pos %d) peeled after clique node %d (pos %d)", pos[11], id, pos[id])
		}
	}
}

// TestIsolatedNodes ensures isolated nodes are created, kept, and
// reported with core 0, even after losing all their edges.
func TestIsolatedNodes(t *testing.T) {
	ix := buildGraph(t, []int64{42}, []coreindex.EdgeOp{coreindex.AddOp(1, 2)})
	if c, ok := ix.Core(42); !ok || c != 0 {
		t.Fatalf("isolated node 42: core %d, ok=%v", c, ok)
	}
	// Node 1 loses its only edge and becomes isolated; it must stay.
	if _, err := ix.ApplyBatch([]coreindex.EdgeOp{coreindex.DelOp(1, 2)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 42} {
		if !ix.HasNode(id) {
			t.Fatalf("node %d disappeared", id)
		}
		if c, _ := ix.Core(id); c != 0 {
			t.Fatalf("node %d: core %d, want 0", id, c)
		}
	}
	d := ix.Decompose()
	if len(d.Order) != 3 {
		t.Fatalf("decomposition dropped isolated nodes: order %v", d.Order)
	}
	for i, id := range d.Order {
		if d.RemDeg[i] != 0 {
			t.Fatalf("node %d: residual degree %d, want 0", id, d.RemDeg[i])
		}
	}
}

// TestDeterministicOrderSameGraphs checks that structurally identical
// graphs built via different op orders still decompose deterministically
// (order is a function of the graph, not of construction history).
func TestDeterministicOrderSameGraphs(t *testing.T) {
	mk := func(reverse bool) coreindex.Decomposition {
		ix := coreindex.New()
		edges := [][2]int64{{1, 2}, {2, 3}, {3, 1}, {3, 4}, {4, 5}, {5, 6}, {6, 4}}
		var ops []coreindex.EdgeOp
		for i := range edges {
			e := edges[i]
			if reverse {
				e = edges[len(edges)-1-i]
			}
			ops = append(ops, coreindex.AddOp(e[0], e[1]))
		}
		if _, err := ix.ApplyBatch(ops); err != nil {
			t.Fatal(err)
		}
		return ix.Build()
	}
	d1, d2 := mk(false), mk(true)
	if !reflect.DeepEqual(d1.Order, d2.Order) || !reflect.DeepEqual(d1.RemDeg, d2.RemDeg) {
		t.Fatalf("order depends on construction history:\n%v\n%v", d1.Order, d2.Order)
	}
}
