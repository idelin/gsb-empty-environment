package objectgraph

import (
	"errors"
	"testing"
)

func TestDiamondSharedNodeCountedOnce(t *testing.T) {
	// 0 -> {1,2}; 1 -> 3; 2 -> 3. Five edges, but only four entities.
	adj := [][]int{{1, 2}, {3}, {3}, nil}
	st, err := AnalyzeGraph(adj, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Reachable) != 4 {
		t.Fatalf("reachable = %d, want 4", len(st.Reachable))
	}
	if st.CyclicNodes != 0 || st.Depth != 3 {
		t.Fatalf("cyclic=%d depth=%d, want 0/3", st.CyclicNodes, st.Depth)
	}
}

func TestDepthIndependentOfOrdering(t *testing.T) {
	// Same graph emitted in two index orders must yield the same depth and
	// cyclic counts.
	adjA := [][]int{{1}, {2}, {3}, {1}} // root 0: SCC {0},{1,2,3}
	stA, err := AnalyzeGraph(adjA, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Same shape with different node numbering: root 0, cycle SCC {1,2,3}.
	adjB := [][]int{{3}, {2}, {3}, {1}}
	stB, err := AnalyzeGraph(adjB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stA.Depth != stB.Depth || stA.CyclicNodes != stB.CyclicNodes {
		t.Fatalf("order dependence: A(depth=%d cyc=%d) B(depth=%d cyc=%d)",
			stA.Depth, stA.CyclicNodes, stB.Depth, stB.CyclicNodes)
	}
	if stA.Depth != 2 || stA.CyclicNodes != 3 {
		t.Fatalf("want depth 2 cyclic 3, got %d/%d", stA.Depth, stA.CyclicNodes)
	}
}

func TestLongDAGNoRecursion(t *testing.T) {
	const n = 50_000
	adj := make([][]int, n)
	for i := 0; i+1 < n; i++ {
		adj[i] = []int{i + 1}
	}
	st, err := AnalyzeGraph(adj, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Depth != n {
		t.Fatalf("depth = %d, want %d", st.Depth, n)
	}
}

func TestInvalidRootAndRef(t *testing.T) {
	if _, err := AnalyzeGraph([][]int{nil}, 1); !errors.Is(err, ErrRootIndex) {
		t.Fatalf("root err = %v", err)
	}
	if _, err := AnalyzeGraph([][]int{{1}}, 0); !errors.Is(err, ErrBadRef) {
		t.Fatalf("ref err = %v", err)
	}
}
