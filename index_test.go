package dagindex

import (
	"fmt"
	"testing"
)

// buildLayeredDAG creates L layers of W nodes each; every node connects to
// every node of the next layer. Returns the index and the layer offsets.
func buildLayeredDAG(t *testing.T, layers, width int) (*Index, [][]int) {
	t.Helper()
	ix := New()
	nodes := make([][]int, layers)
	for l := 0; l < layers; l++ {
		nodes[l] = make([]int, width)
		for w := 0; w < width; w++ {
			nodes[l][w] = ix.AddNode()
		}
	}
	for l := 0; l+1 < layers; l++ {
		for _, u := range nodes[l] {
			for _, v := range nodes[l+1] {
				if err := ix.AddEdge(u, v); err != nil {
					t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
				}
			}
		}
	}
	return ix, nodes
}

// referenceReachable computes reachability with a plain BFS, used as the
// ground truth to validate the bitmap index.
func referenceReachable(ix *Index, u, v int) bool {
	seen := map[int]bool{u: true}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		if x == v {
			return true
		}
		for _, w := range ix.succ[x] {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return false
}

func checkAllPairs(t *testing.T, ix *Index) {
	t.Helper()
	for u := 0; u < ix.Size(); u++ {
		for v := 0; v < ix.Size(); v++ {
			got := ix.Reachable(u, v)
			want := referenceReachable(ix, u, v)
			if got != want {
				t.Fatalf("Reachable(%d,%d)=%v, BFS says %v", u, v, got, want)
			}
		}
	}
}

func TestLayeredDAGReachability(t *testing.T) {
	ix, nodes := buildLayeredDAG(t, 4, 3)

	// Levels must increase layer by layer.
	for l := 0; l < 4; l++ {
		for _, u := range nodes[l] {
			if ix.Level(u) != l {
				t.Fatalf("node %d: level=%d, want %d", u, ix.Level(u), l)
			}
		}
	}
	// Fully connected adjacent layers => any node in an earlier layer
	// reaches every node in a later layer, and nothing backwards.
	for l := 0; l < 4; l++ {
		for _, u := range nodes[l] {
			for k := 0; k < 4; k++ {
				for _, v := range nodes[k] {
					want := k > l || (k == l && u == v)
					if got := ix.Reachable(u, v); got != want {
						t.Fatalf("Reachable(%d,%d)=%v, want %v", u, v, got, want)
					}
				}
			}
		}
	}
	checkAllPairs(t, ix)
}

func TestCrossLayerEdgeUpdatesDownstream(t *testing.T) {
	// Two disconnected chains: 0->1->2 and 3->4->5.
	ix := New()
	for i := 0; i < 6; i++ {
		ix.AddNode()
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {3, 4}, {4, 5}} {
		if err := ix.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if ix.Reachable(0, 5) || ix.Reachable(1, 4) {
		t.Fatal("chains must be disconnected before the cross edge")
	}

	// Cross-layer edge 0->4: 0 must now inherit {4,5}, and so must
	// nothing else (0 is a source). Levels of 4,5 must be raised.
	if err := ix.AddEdge(0, 4); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{4, 5} {
		if !ix.Reachable(0, v) {
			t.Fatalf("after cross edge, 0 must reach %d", v)
		}
	}
	if ix.Reachable(3, 0) || ix.Reachable(5, 0) {
		t.Fatal("cross edge must not create backward reachability")
	}
	if ix.Level(4) != 1 || ix.Level(5) != 2 {
		t.Fatalf("levels after cross edge: level(4)=%d level(5)=%d, want 1 and 2",
			ix.Level(4), ix.Level(5))
	}

	// Edge 1->3 propagates {3,4,5} to 1 and then to 0 (upstream chain).
	if err := ix.AddEdge(1, 3); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{2, 3, 4, 5} {
		if !ix.Reachable(0, v) || !ix.Reachable(1, v) {
			t.Fatalf("after edge 1->3, 0 and 1 must reach %d", v)
		}
	}
	if ix.Reachable(2, 3) {
		t.Fatal("node 2 is not an ancestor of 1 and must stay isolated from 3")
	}
	checkAllPairs(t, ix)
}

func TestCycleRejection(t *testing.T) {
	ix := New()
	for i := 0; i < 4; i++ {
		ix.AddNode()
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}} {
		if err := ix.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][2]int{{3, 0}, {2, 0}, {3, 1}, {0, 0}} {
		if err := ix.AddEdge(e[0], e[1]); err == nil {
			t.Fatalf("edge %d->%d must be rejected (cycle)", e[0], e[1])
		}
	}
	// Index must be unchanged and still consistent.
	checkAllPairs(t, ix)
	if ix.Stats().Edges != 3 {
		t.Fatalf("rejected edges must not be stored, edges=%d", ix.Stats().Edges)
	}
}

func TestRedundantEdgeIsNoop(t *testing.T) {
	ix := New()
	for i := 0; i < 3; i++ {
		ix.AddNode()
	}
	if err := ix.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := ix.AddEdge(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := ix.AddEdge(0, 2); err != nil {
		t.Fatal(err) // first insert of a transitively-covered edge is fine
	}
	before := ix.Stats()
	if err := ix.AddEdge(0, 2); err != nil {
		t.Fatal(err) // second insert is a no-op
	}
	if got := ix.Stats(); got != before {
		t.Fatalf("redundant edge changed the index: %+v -> %+v", before, got)
	}
}

func TestMemoryCompressionBound(t *testing.T) {
	// A long chain: N^2/2 reachable pairs, but each node's bitmap is a
	// contiguous run of bits, so sparse-word storage stays well below the
	// dense matrix.
	const n = 512
	ix := New()
	for i := 0; i < n; i++ {
		ix.AddNode()
	}
	for i := 0; i+1 < n; i++ {
		if err := ix.AddEdge(i, i+1); err != nil {
			t.Fatal(err)
		}
	}
	s := ix.Stats()
	if s.ReachablePairs != n*(n+1)/2 {
		t.Fatalf("reachable pairs=%d, want %d", s.ReachablePairs, n*(n+1)/2)
	}
	if s.StoredWords >= s.DenseWords {
		t.Fatalf("compression violated: stored %d words >= dense %d words",
			s.StoredWords, s.DenseWords)
	}
	t.Logf("chain n=%d: stored=%d dense=%d words (%.1f%% of dense)",
		n, s.StoredWords, s.DenseWords, 100*float64(s.StoredWords)/float64(s.DenseWords))
	checkAllPairs(t, ix)
}

func TestRandomDAGAgainstBFS(t *testing.T) {
	// Deterministic pseudo-random DAG: nodes 0..199, edges only i->j (i<j)
	// with an LCG, inserted in random order.
	const n = 200
	ix := New()
	for i := 0; i < n; i++ {
		ix.AddNode()
	}
	seed := uint64(42)
	next := func() uint64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 33 }
	var edges [][2]int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if next()%100 < 3 {
				edges = append(edges, [2]int{i, j})
			}
		}
	}
	// Shuffle insertion order.
	for i := len(edges) - 1; i > 0; i-- {
		j := int(next() % uint64(i+1))
		edges[i], edges[j] = edges[j], edges[i]
	}
	for _, e := range edges {
		if err := ix.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("edge %v: %v", e, err)
		}
	}
	checkAllPairs(t, ix)
}

func Example() {
	ix := New()
	for i := 0; i < 4; i++ {
		ix.AddNode()
	}
	// Layered edges: 0->1, 0->2, 1->3, 2->3.
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		_ = ix.AddEdge(e[0], e[1])
	}
	fmt.Println(ix.Reachable(0, 3)) // via 1 or 2
	fmt.Println(ix.Reachable(3, 0))
	fmt.Println(ix.Reachable(1, 2)) // same layer, no path
	// Output:
	// true
	// false
	// false
}
