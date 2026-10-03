package dagreach

import (
	"math/rand"
	"testing"
)

// referenceReachable answers reachability by plain BFS over adjacency.
func referenceReachable(children [][]int, u, v int) bool {
	seen := make([]bool, len(children))
	stack := []int{u}
	seen[u] = true
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == v {
			return true
		}
		for _, c := range children[x] {
			if !seen[c] {
				seen[c] = true
				stack = append(stack, c)
			}
		}
	}
	return false
}

// verifyAllPairs checks every ordered pair against the BFS reference.
func verifyAllPairs(t *testing.T, idx *Indexer, children [][]int) {
	t.Helper()
	n := idx.Size()
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			want := referenceReachable(children, u, v)
			if got := idx.Reachable(u, v); got != want {
				t.Fatalf("Reachable(%d,%d)=%v, want %v", u, v, got, want)
			}
		}
	}
}

// layeredEdge appends an edge to the adjacency mirror used by the reference.
func addMirror(children [][]int, u, v int) {
	children[u] = append(children[u], v)
}

func TestLayeredDAGAllPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const levels = 6
	const width = 12

	idx := NewIndexer(levels * width)
	children := make([][]int, 0, levels*width)
	layerOf := make([]int, 0, levels*width)
	for l := 0; l < levels; l++ {
		for w := 0; w < width; w++ {
			id := idx.AddNode()
			children = append(children, nil)
			layerOf = append(layerOf, l)
			_ = id
		}
	}
	nodeAt := func(l, w int) int { return l*width + w }

	// Edges between adjacent layers plus random cross-layer (skip) edges.
	for l := 0; l+1 < levels; l++ {
		for w := 0; w < width; w++ {
			u := nodeAt(l, w)
			for k := 0; k < 2; k++ {
				v := nodeAt(l+1, rng.Intn(width))
				if err := idx.AddEdge(u, v); err != nil {
					t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
				}
				addMirror(children, u, v)
			}
			if rng.Float64() < 0.3 && l+2 < levels {
				v := nodeAt(l+2+rng.Intn(levels-l-2), rng.Intn(width))
				if err := idx.AddEdge(u, v); err != nil {
					t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
				}
				addMirror(children, u, v)
			}
		}
	}

	verifyAllPairs(t, idx, children)

	// Topological layering invariant: parent level < child level.
	for u := 0; u < idx.Size(); u++ {
		for _, v := range children[u] {
			if idx.Level(u) >= idx.Level(v) {
				t.Fatalf("level invariant broken: level[%d]=%d, level[%d]=%d",
					u, idx.Level(u), v, idx.Level(v))
			}
		}
	}
}

func TestIncrementalCrossLayerEdgeUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 60

	idx := NewIndexer(n)
	children := make([][]int, n)
	for i := 0; i < n; i++ {
		idx.AddNode()
	}

	// Insert edges one at a time (always from lower id to higher id to
	// stay acyclic), verifying the full reachability matrix after each.
	edges := 0
	for edges < 150 {
		u := rng.Intn(n - 1)
		v := u + 1 + rng.Intn(n-1-u)
		if idx.Reachable(u, v) && contains(children[u], v) {
			continue
		}
		if err := idx.AddEdge(u, v); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
		}
		if !contains(children[u], v) {
			addMirror(children, u, v)
		}
		edges++
		if edges%10 == 0 {
			verifyAllPairs(t, idx, children)
		}
	}
	verifyAllPairs(t, idx, children)
}

func contains(s []int, x int) bool {
	for _, e := range s {
		if e == x {
			return true
		}
	}
	return false
}

func TestCycleRejection(t *testing.T) {
	idx := NewIndexer(4)
	for i := 0; i < 4; i++ {
		idx.AddNode()
	}
	must := func(u, v int) {
		if err := idx.AddEdge(u, v); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
		}
	}
	must(0, 1)
	must(1, 2)
	must(0, 3)

	if err := idx.AddEdge(2, 0); err != ErrCycle {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
	if err := idx.AddEdge(2, 2); err != ErrSelfLoop {
		t.Fatalf("expected ErrSelfLoop, got %v", err)
	}
	if err := idx.AddEdge(1, 9); err != ErrNodeRange {
		t.Fatalf("expected ErrNodeRange, got %v", err)
	}
	// Graph must be unchanged after rejected insertions.
	if idx.EdgeCount() != 3 {
		t.Fatalf("EdgeCount=%d, want 3", idx.EdgeCount())
	}
	if !idx.Reachable(0, 2) || idx.Reachable(2, 0) {
		t.Fatal("reachability corrupted after rejected edge")
	}
}

func TestDuplicateEdgeNoop(t *testing.T) {
	idx := NewIndexer(2)
	idx.AddNode()
	idx.AddNode()
	if err := idx.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := idx.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	if idx.EdgeCount() != 1 {
		t.Fatalf("EdgeCount=%d, want 1", idx.EdgeCount())
	}
}

func TestCompressedIndexEquivalenceAndMemory(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const n = 200

	idx := NewIndexer(n)
	children := make([][]int, n)
	for i := 0; i < n; i++ {
		idx.AddNode()
	}
	// Sparse DAG: each node points at a few higher-numbered nodes.
	for u := 0; u < n-1; u++ {
		for k := 0; k < 2; k++ {
			v := u + 1 + rng.Intn(n-1-u)
			if err := idx.AddEdge(u, v); err != nil {
				t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
			}
			if !contains(children[u], v) {
				addMirror(children, u, v)
			}
		}
	}

	comp := idx.Compress()
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			if comp.Reachable(u, v) != idx.Reachable(u, v) {
				t.Fatalf("compressed/dense mismatch at (%d,%d)", u, v)
			}
		}
	}

	dense := idx.MemoryBytes()
	compressed := comp.MemoryBytes()
	t.Logf("dense=%d bytes, compressed=%d bytes, ratio=%.2fx, nonZeroWords=%d/%d",
		dense, compressed, comp.CompressionRatio(dense),
		comp.NonZeroWords(), n*((n+63)/64))
	if compressed >= dense {
		t.Fatalf("compressed memory %d not below dense %d", compressed, dense)
	}
}

func TestCrossLayerEdgeBitmapPropagation(t *testing.T) {
	// Chain 0->1->2 and chain 3->4->5, then bridge them with a
	// cross-layer edge 1->4 and check the merged closures.
	idx := NewIndexer(6)
	for i := 0; i < 6; i++ {
		idx.AddNode()
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {3, 4}, {4, 5}} {
		if err := idx.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if idx.Reachable(0, 5) {
		t.Fatal("0 must not reach 5 before the bridge edge")
	}
	if err := idx.AddEdge(1, 4); err != nil {
		t.Fatal(err)
	}
	for _, u := range []int{0, 1} {
		for _, v := range []int{4, 5} {
			if !idx.Reachable(u, v) {
				t.Fatalf("after bridge: %d must reach %d", u, v)
			}
		}
	}
	if idx.Reachable(3, 2) || idx.Reachable(5, 0) {
		t.Fatal("bridge edge leaked reachability backwards")
	}
}

func BenchmarkReachableQuery(b *testing.B) {
	const n = 4096
	idx := NewIndexer(n)
	for i := 0; i < n; i++ {
		idx.AddNode()
	}
	for u := 0; u+1 < n; u++ {
		if err := idx.AddEdge(u, u+1); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u := i % n
		v := (i * 7919) % n
		if !idx.Reachable(u, v) && u <= v {
			b.Fatalf("unexpected miss at (%d,%d)", u, v)
		}
	}
}
