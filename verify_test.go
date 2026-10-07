package twcheck

import "testing"

func pathGraph(n int) Graph {
	g := Graph{N: n}
	for i := 0; i+1 < n; i++ {
		g.Edges = append(g.Edges, [2]int{i, i + 1})
	}
	return g
}

func TestValidPathDecomposition(t *testing.T) {
	g := pathGraph(4) // 0-1-2-3
	td := Decomposition{
		Bags: [][]int{{0, 1}, {1, 2}, {2, 3}},
		Adj:  [][]int{{1}, {0, 2}, {1}},
	}
	w, v := Verify(g, td)
	if v != nil {
		t.Fatalf("expected valid, got %v", v)
	}
	if w != 1 {
		t.Fatalf("width = %d, want 1", w)
	}
}

func TestEmptyGraphSingleEmptyBag(t *testing.T) {
	w, v := Verify(Graph{N: 0}, Decomposition{Bags: [][]int{{}}, Adj: [][]int{{}}})
	if v != nil {
		t.Fatalf("expected valid, got %v", v)
	}
	if w != -1 {
		t.Fatalf("width = %d, want -1", w)
	}
}

func TestEmptyGraphNoBags(t *testing.T) {
	_, v := Verify(Graph{N: 0}, Decomposition{})
	if v == nil || v.Kind != ViolationNotTree {
		t.Fatalf("got %v, want ViolationNotTree", v)
	}
}

func TestEdgeNotCovered(t *testing.T) {
	g := Graph{N: 3, Edges: [][2]int{{0, 1}, {1, 2}, {0, 2}}}
	td := Decomposition{
		Bags: [][]int{{0, 1}, {1, 2}},
		Adj:  [][]int{{1}, {0}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationEdgeNotCovered {
		t.Fatalf("got %v, want ViolationEdgeNotCovered", v)
	}
	if v.Edge != [2]int{0, 2} {
		t.Fatalf("violation edge = %v, want [0 2]", v.Edge)
	}
}

func TestDisconnectedBagGraph(t *testing.T) {
	g := pathGraph(2)
	td := Decomposition{
		Bags: [][]int{{0, 1}, {0, 1}},
		Adj:  [][]int{{}, {}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationNotTree {
		t.Fatalf("got %v, want ViolationNotTree", v)
	}
}

func TestCyclicBagGraph(t *testing.T) {
	g := Graph{N: 3, Edges: [][2]int{{0, 1}, {1, 2}, {0, 2}}}
	td := Decomposition{
		Bags: [][]int{{0, 1, 2}, {0, 1, 2}, {0, 1, 2}},
		Adj:  [][]int{{1, 2}, {0, 2}, {0, 1}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationNotTree {
		t.Fatalf("got %v, want ViolationNotTree", v)
	}
}

func TestVertexMissing(t *testing.T) {
	g := Graph{N: 3, Edges: [][2]int{{0, 1}}}
	td := Decomposition{
		Bags: [][]int{{0, 1}},
		Adj:  [][]int{{}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationVertexMissing || v.Vertex != 2 {
		t.Fatalf("got %v, want ViolationVertexMissing vertex 2", v)
	}
}

// Running intersection: vertex 0 sits in bags 0 and 2, separated by
// bag 1 which lacks 0. Adjacent bags pairwise intersect, so a
// pairwise-only check would wrongly accept this.
func TestRunningIntersectionCut(t *testing.T) {
	g := Graph{N: 3, Edges: [][2]int{{0, 1}, {1, 2}, {0, 2}}}
	td := Decomposition{
		Bags: [][]int{{0, 1}, {1, 2}, {0, 2}},
		Adj:  [][]int{{1}, {0, 2}, {1}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationNotConnected {
		t.Fatalf("got %v, want ViolationNotConnected", v)
	}
	if v.Vertex != 0 && v.Vertex != 2 {
		t.Fatalf("violation vertex = %d, want 0 or 2", v.Vertex)
	}
	if v.Bags != [2]int{1, 2} && v.Bags != [2]int{0, 1} {
		t.Fatalf("witness tree edge = %v", v.Bags)
	}
}

// Star case: pairwise bag intersections are all non-empty (all share
// vertex 0) and the decomposition is actually valid.
func TestStarValid(t *testing.T) {
	g := Graph{N: 4, Edges: [][2]int{{0, 1}, {0, 2}, {0, 3}}}
	td := Decomposition{
		Bags: [][]int{{0, 1}, {0, 2}, {0, 3}},
		Adj:  [][]int{{1, 2}, {0}, {0}},
	}
	w, v := Verify(g, td)
	if v != nil {
		t.Fatalf("expected valid, got %v", v)
	}
	if w != 1 {
		t.Fatalf("width = %d, want 1", w)
	}
}

func TestBadVertexInBag(t *testing.T) {
	g := pathGraph(2)
	td := Decomposition{
		Bags: [][]int{{0, 1, 5}},
		Adj:  [][]int{{}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationBadIndex {
		t.Fatalf("got %v, want ViolationBadIndex", v)
	}
}

func TestAsymmetricAdjacency(t *testing.T) {
	g := pathGraph(2)
	td := Decomposition{
		Bags: [][]int{{0, 1}, {0, 1}},
		Adj:  [][]int{{1}, {}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationNotTree {
		t.Fatalf("got %v, want ViolationNotTree", v)
	}
}

func TestWidthLargerBags(t *testing.T) {
	// Triangle needs a bag of size 3 -> width 2.
	g := Graph{N: 3, Edges: [][2]int{{0, 1}, {1, 2}, {0, 2}}}
	td := Decomposition{
		Bags: [][]int{{0, 1, 2}},
		Adj:  [][]int{{}},
	}
	w, v := Verify(g, td)
	if v != nil {
		t.Fatalf("expected valid, got %v", v)
	}
	if w != 2 {
		t.Fatalf("width = %d, want 2", w)
	}
}

// Deeper cut test: vertex appears in two distant leaves of a larger
// tree; the failing cut may be any edge on the path between them.
func TestDeepCutViolation(t *testing.T) {
	g := Graph{N: 5, Edges: [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {0, 4}}}
	td := Decomposition{
		Bags: [][]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {0, 4}},
		Adj:  [][]int{{1}, {0, 2}, {1, 3}, {2, 4}, {3}},
	}
	_, v := Verify(g, td)
	if v == nil || v.Kind != ViolationNotConnected {
		t.Fatalf("got %v, want ViolationNotConnected", v)
	}
}
