package treedecomp

import "testing"

func TestValidPathDecomposition(t *testing.T) {
	// Path graph 0-1-2-3 with the canonical path decomposition.
	g := Graph{N: 4, Edges: []Edge{{0, 1}, {1, 2}, {2, 3}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {1, 2}, {2, 3}},
		Edges: []TreeEdge{{0, 1}, {1, 2}},
	}
	res := Validate(g, d)
	if !res.Valid {
		t.Fatalf("expected valid, got violation: %v", res.Violation)
	}
	if res.Width != 1 {
		t.Fatalf("width = %d, want 1", res.Width)
	}
}

func TestEmptyGraphSingleEmptyBag(t *testing.T) {
	g := Graph{N: 0}
	d := Decomposition{Bags: [][]int{{}}}
	res := Validate(g, d)
	if !res.Valid {
		t.Fatalf("expected valid, got violation: %v", res.Violation)
	}
	if res.Width != -1 {
		t.Fatalf("width = %d, want -1", res.Width)
	}
}

func TestEmptyGraphNonemptyBagRejected(t *testing.T) {
	g := Graph{N: 0}
	d := Decomposition{Bags: [][]int{{0}}}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationVertexOutOfRange {
		t.Fatalf("got %+v, want vertex-out-of-range violation", res)
	}
}

func TestNoBagsRejected(t *testing.T) {
	res := Validate(Graph{N: 0}, Decomposition{})
	if res.Valid || res.Violation.Kind != ViolationBagGraphNotTree {
		t.Fatalf("got %+v, want not-a-tree violation", res)
	}
}

func TestEdgeNotCovered(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{{0, 1}, {1, 2}, {0, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {1, 2}},
		Edges: []TreeEdge{{0, 1}},
	}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationEdgeNotCovered {
		t.Fatalf("got %+v, want edge-not-covered violation", res)
	}
	if res.Violation.Edge != (Edge{0, 2}) {
		t.Fatalf("witness edge = %+v, want {0 2}", res.Violation.Edge)
	}
}

// The key case: bags {0,1},{1,2},{0,2} in a path. Every pair of
// adjacent bags intersects, yet vertex 0 lives in bags 0 and 2, which
// are not connected. Only the tree-edge deletion partition test catches
// this; pairwise intersection checks would wrongly accept it.
func TestRunningIntersectionPairwiseIntersectionIsNotEnough(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{{0, 1}, {1, 2}, {0, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {1, 2}, {0, 2}},
		Edges: []TreeEdge{{0, 1}, {1, 2}},
	}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationRunningIntersection {
		t.Fatalf("got %+v, want running-intersection violation", res)
	}
	v := res.Violation
	if v.Vertex != 0 {
		t.Fatalf("witness vertex = %d, want 0", v.Vertex)
	}
	if v.TreeEdge != (TreeEdge{0, 1}) && v.TreeEdge != (TreeEdge{1, 2}) {
		t.Fatalf("witness tree edge = %+v, want {0 1} or {1 2}", v.TreeEdge)
	}
	if v.LeftBag < 0 || v.RightBag < 0 || v.LeftBag == v.RightBag {
		t.Fatalf("witness bags = %d,%d, want two distinct bags", v.LeftBag, v.RightBag)
	}
}

func TestDisconnectedBagGraph(t *testing.T) {
	g := Graph{N: 2, Edges: []Edge{{0, 1}}}
	d := Decomposition{
		Bags:  [][]int{{0}, {1}},
		Edges: nil,
	}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationBagGraphNotTree {
		t.Fatalf("got %+v, want not-a-tree violation", res)
	}
}

func TestCyclicBagGraph(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{{0, 1}, {1, 2}, {0, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1, 2}, {0, 1, 2}, {0, 1, 2}},
		Edges: []TreeEdge{{0, 1}, {1, 2}, {2, 0}},
	}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationBagGraphNotTree {
		t.Fatalf("got %+v, want not-a-tree violation", res)
	}
}

func TestDuplicateTreeEdge(t *testing.T) {
	g := Graph{N: 2, Edges: []Edge{{0, 1}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {0, 1}},
		Edges: []TreeEdge{{0, 1}, {1, 0}},
	}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationBagEdgeOutOfRange {
		t.Fatalf("got %+v, want invalid-tree-edge violation", res)
	}
}

func TestVertexMissing(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{{0, 1}}}
	d := Decomposition{Bags: [][]int{{0, 1}}}
	res := Validate(g, d)
	if res.Valid || res.Violation.Kind != ViolationVertexMissing {
		t.Fatalf("got %+v, want vertex-missing violation", res)
	}
	if res.Violation.Vertex != 2 {
		t.Fatalf("witness vertex = %d, want 2", res.Violation.Vertex)
	}
}

func TestWidthReturnedWithViolation(t *testing.T) {
	// Running-intersection violation; width is still reported.
	g := Graph{N: 3, Edges: []Edge{{0, 1}, {1, 2}, {0, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {1, 2}, {0, 2}},
		Edges: []TreeEdge{{0, 1}, {1, 2}},
	}
	res := Validate(g, d)
	if res.Valid {
		t.Fatal("expected violation")
	}
	if res.Width != 1 {
		t.Fatalf("width = %d, want 1", res.Width)
	}
}

// A vertex appearing on both sides of a cut is fine as long as it sits
// in both endpoint bags of the cut edge.
func TestVertexSpanningCutViaEndpointBags(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{{0, 1}, {1, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1}, {1, 2}, {1}},
		Edges: []TreeEdge{{0, 1}, {1, 2}},
	}
	res := Validate(g, d)
	if !res.Valid {
		t.Fatalf("expected valid, got violation: %v", res.Violation)
	}
}

func TestValidStarDecomposition(t *testing.T) {
	// Triangle plus isolated vertex: one big central bag works.
	g := Graph{N: 4, Edges: []Edge{{0, 1}, {1, 2}, {0, 2}}}
	d := Decomposition{
		Bags:  [][]int{{0, 1, 2}, {3}},
		Edges: []TreeEdge{{0, 1}},
	}
	res := Validate(g, d)
	if !res.Valid {
		t.Fatalf("expected valid, got violation: %v", res.Violation)
	}
	if res.Width != 2 {
		t.Fatalf("width = %d, want 2", res.Width)
	}
}
