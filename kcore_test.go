package kcore

import (
	"math/rand"
	"reflect"
	"testing"
)

// buildAcceptanceGraph builds the acceptance-scenario graph:
// a dense 5-clique (core 4), a K4-e cluster (core 2), a cycle
// (core 2) and a dangling chain (core 1).
func buildAcceptanceGraph() *Graph {
	g := New()
	clique := []int{1, 2, 3, 4, 5}
	for i, a := range clique {
		for _, b := range clique[i+1:] {
			g.AddEdge(a, b)
		}
	}
	for _, e := range [][2]int{{10, 11}, {10, 12}, {10, 13}, {11, 12}, {11, 13}} {
		g.AddEdge(e[0], e[1])
	}
	for _, e := range [][2]int{{20, 21}, {21, 22}, {22, 23}, {23, 20}} {
		g.AddEdge(e[0], e[1])
	}
	g.AddEdge(1, 30)
	g.AddEdge(30, 31)
	g.AddEdge(31, 32)
	return g
}

func TestBatchDecomposition(t *testing.T) {
	g := buildAcceptanceGraph()
	g.Decompose()

	want := map[int]int{
		1: 4, 2: 4, 3: 4, 4: 4, 5: 4, // 5-clique
		10: 2, 11: 2, 12: 2, 13: 2, // K4 minus an edge
		20: 2, 21: 2, 22: 2, 23: 2, // cycle
		30: 1, 31: 1, 32: 1, // dangling chain
	}
	for v, c := range want {
		if got := g.CoreNumber(v); got != c {
			t.Errorf("core(%d) = %d, want %d", v, got, c)
		}
	}
	if mc := g.MaxCore(); mc != 4 {
		t.Errorf("MaxCore = %d, want 4", mc)
	}
}

func TestLayeredSubgraphs(t *testing.T) {
	g := buildAcceptanceGraph()

	all := []int{1, 2, 3, 4, 5, 10, 11, 12, 13, 20, 21, 22, 23, 30, 31, 32}
	dense := []int{1, 2, 3, 4, 5}
	twoCore := []int{1, 2, 3, 4, 5, 10, 11, 12, 13, 20, 21, 22, 23}

	cases := map[int][]int{
		1: all,
		2: twoCore,
		3: dense,
		4: dense,
	}
	for k := 1; k <= 4; k++ {
		if got := g.KCoreNodes(k); !reflect.DeepEqual(got, cases[k]) {
			t.Errorf("%d-core = %v, want %v", k, got, cases[k])
		}
	}

	layers := g.Layers()
	if got := layers[1]; !reflect.DeepEqual(got, []int{30, 31, 32}) {
		t.Errorf("coreness-1 layer = %v", got)
	}
	if got := layers[4]; !reflect.DeepEqual(got, dense) {
		t.Errorf("coreness-4 layer = %v", got)
	}
}

func TestBridgeEdgePromotionIsLocal(t *testing.T) {
	g := buildAcceptanceGraph()
	g.Decompose()

	// Bridge the missing edge: cluster B becomes K4, so all four
	// vertices rise from coreness 2 to 3.
	g.AddEdge(12, 13)
	for _, v := range []int{10, 11, 12, 13} {
		if got := g.CoreNumber(v); got != 3 {
			t.Errorf("after bridge core(%d) = %d, want 3", v, got)
		}
	}
	if got := g.KCoreNodes(3); !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 10, 11, 12, 13}) {
		t.Errorf("3-core after bridge = %v", got)
	}

	localWork := g.LastUpdateWork()
	g.Decompose()
	globalWork := g.LastUpdateWork()
	if localWork >= globalWork {
		t.Fatalf("local work %d not smaller than global work %d", localWork, globalWork)
	}
	if localWork > 4 {
		t.Errorf("local update visited %d vertices, want at most the 4 affected ones", localWork)
	}
	t.Logf("bridge edge: local update visited %d vertices vs %d for global recompute",
		localWork, globalWork)

	// Removing the bridge demotes the cluster back, again locally.
	g.RemoveEdge(12, 13)
	for _, v := range []int{10, 11, 12, 13} {
		if got := g.CoreNumber(v); got != 2 {
			t.Errorf("after unbridge core(%d) = %d, want 2", v, got)
		}
	}
	if w := g.LastUpdateWork(); w > 4 {
		t.Errorf("local delete visited %d vertices, want <= 4", w)
	}
}

// TestIncrementalMatchesBatch fuzzes insertions and deletions,
// checking after every operation that the incrementally maintained
// core numbers equal a full recomputation from scratch.
func TestIncrementalMatchesBatch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 120
	const ops = 600

	g := New()
	mirror := New()
	for i := 0; i < n; i++ {
		g.AddNode(i)
		mirror.AddNode(i)
	}
	// Seed with a random initial graph.
	for i := 0; i < 200; i++ {
		u, v := rng.Intn(n), rng.Intn(n)
		g.AddEdge(u, v)
		mirror.AddEdge(u, v)
	}
	g.Decompose()

	for i := 0; i < ops; i++ {
		u, v := rng.Intn(n), rng.Intn(n)
		if rng.Intn(2) == 0 {
			g.AddEdge(u, v)
			mirror.AddEdge(u, v)
		} else {
			g.RemoveEdge(u, v)
			mirror.RemoveEdge(u, v)
		}
		mirror.Decompose() // authoritative full recompute
		got, want := g.CoreNumbers(), mirror.CoreNumbers()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("op %d (%d,%d): incremental coreness diverges from batch", i, u, v)
		}
	}
}

// TestLargeSparseGraphLocalUpdate shows that on a large sparse graph a
// single bridging edge only re-propagates through a tiny neighbourhood.
func TestLargeSparseGraphLocalUpdate(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 30000

	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(i)
	}
	// Sparse random edges, avg degree ~4.
	for i := 0; i < 2*n; i++ {
		g.AddEdge(rng.Intn(n), rng.Intn(n))
	}
	// A small K4-e gadget, disjoint from the random part.
	base := n
	for _, e := range [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}} {
		g.AddEdge(base+e[0], base+e[1])
	}
	g.Decompose()
	if c := g.CoreNumber(base); c != 2 {
		t.Fatalf("gadget coreness = %d, want 2", c)
	}

	// Bridge the gadget: exactly its 4 vertices are affected.
	g.AddEdge(base+2, base+3)
	localWork := g.LastUpdateWork()
	if localWork != 4 {
		t.Errorf("local update visited %d vertices, want exactly 4", localWork)
	}
	for i := 0; i < 4; i++ {
		if c := g.CoreNumber(base + i); c != 3 {
			t.Errorf("core(%d) = %d, want 3", base+i, c)
		}
	}

	g.Decompose()
	globalWork := g.LastUpdateWork()
	if localWork*1000 >= globalWork {
		t.Errorf("local work %d not far below global work %d", localWork, globalWork)
	}
	t.Logf("n=%d: local update visited %d vertices, global recompute visited %d (%.0fx)",
		g.NodeCount(), localWork, globalWork, float64(globalWork)/float64(localWork))
}

func TestEdgeCases(t *testing.T) {
	g := New()
	g.AddEdge(1, 1) // self-loop ignored
	g.AddEdge(1, 2)
	g.AddEdge(1, 2) // duplicate ignored
	if g.EdgeCount() != 1 {
		t.Fatalf("EdgeCount = %d, want 1", g.EdgeCount())
	}
	if c := g.CoreNumber(1); c != 1 {
		t.Errorf("core(1) = %d, want 1", c)
	}
	g.RemoveEdge(1, 2)
	g.RemoveEdge(1, 2) // no-op
	if c := g.CoreNumber(1); c != 0 {
		t.Errorf("core(1) after removal = %d, want 0", c)
	}
	if c := g.CoreNumber(999); c != 0 {
		t.Errorf("core of unknown vertex = %d, want 0", c)
	}
}
