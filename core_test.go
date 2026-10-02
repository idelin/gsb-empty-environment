package kcore

import (
	"math/rand"
	"reflect"
	"testing"
)

// buildScenarioGraph builds the acceptance-test graph:
//
//	dense subnet: clique {0..4}                -> core 4
//	prism minus one rung: {10..15}             -> core 2
//	pendant chain 20-21-22 hanging off 0       -> core 1
//	isolated vertex 30                         -> core 0
//
// Adding the missing rung (12,15) turns the prism into a 3-regular
// graph, promoting all six vertices from core 2 to core 3.
func buildScenarioGraph() *Graph {
	g := NewGraph()
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			g.AddEdge(i, j)
		}
	}
	// triangular prism: triangles (10,11,12) and (13,14,15),
	// rungs (10,13) and (11,14); rung (12,15) is left out on purpose.
	for _, e := range [][2]int{
		{10, 11}, {11, 12}, {10, 12},
		{13, 14}, {14, 15}, {13, 15},
		{10, 13}, {11, 14},
	} {
		g.AddEdge(e[0], e[1])
	}
	g.AddEdge(0, 20)
	g.AddEdge(20, 21)
	g.AddEdge(21, 22)
	g.AddVertex(30)
	return g
}

func wantCores() map[int]int {
	want := map[int]int{30: 0, 20: 1, 21: 1, 22: 1}
	for i := 0; i < 5; i++ {
		want[i] = 4
	}
	for v := 10; v <= 15; v++ {
		want[v] = 2
	}
	return want
}

func TestCoreNumbers(t *testing.T) {
	g := buildScenarioGraph()
	got := CoreNumbers(g)
	if !reflect.DeepEqual(got, wantCores()) {
		t.Fatalf("core numbers mismatch:\n got %v\nwant %v", got, wantCores())
	}
}

func TestPeelKCoreLayeredSubgraphs(t *testing.T) {
	g := buildScenarioGraph()
	cores := CoreNumbers(g)
	maxK := MaxCore(cores)
	if maxK != 4 {
		t.Fatalf("MaxCore = %d, want 4", maxK)
	}
	// Peeling must agree with the core numbers for every layer 1..4.
	for k := 1; k <= 4; k++ {
		keep := PeelKCore(g, k)
		for v := 0; v <= 30; v++ {
			want := cores[v] >= k
			if keep[v] != want {
				t.Fatalf("k=%d: vertex %d in k-core = %v, want %v", k, v, keep[v], want)
			}
		}
	}
	// Induced subgraph sizes per layer.
	sizes := map[int]int{1: 14, 2: 11, 3: 5, 4: 5}
	for k, want := range sizes {
		sub := KCoreSubgraph(g, k)
		if sub.NumVertices() != want {
			t.Fatalf("%d-core has %d vertices, want %d", k, sub.NumVertices(), want)
		}
	}
	// The 4-core is exactly the clique: 5 vertices, 10 edges.
	if sub := KCoreSubgraph(g, 4); sub.NumEdges() != 10 {
		t.Fatalf("4-core has %d edges, want 10", sub.NumEdges())
	}
}

func TestLayers(t *testing.T) {
	g := buildScenarioGraph()
	layers := Layers(CoreNumbers(g))
	want := map[int][]int{
		0: {30},
		1: {20, 21, 22},
		2: {10, 11, 12, 13, 14, 15},
		4: {0, 1, 2, 3, 4},
	}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers mismatch:\n got %v\nwant %v", layers, want)
	}
}

func TestIncrementalInsertBridgeEdge(t *testing.T) {
	g := buildScenarioGraph()
	m := NewMaintainer(g)

	// Bridge edge completing the prism: local cores 2 -> 3.
	m.InsertEdge(12, 15)

	for v := 10; v <= 15; v++ {
		if got := m.Core(v); got != 3 {
			t.Fatalf("core(%d) = %d, want 3 after bridge edge", v, got)
		}
	}
	for i := 0; i < 5; i++ {
		if got := m.Core(i); got != 4 {
			t.Fatalf("core(%d) = %d, want 4 (clique untouched)", i, got)
		}
	}
	for _, v := range []int{20, 21, 22} {
		if got := m.Core(v); got != 1 {
			t.Fatalf("core(%d) = %d, want 1 (chain untouched)", v, got)
		}
	}
	// Locality: only the 6 prism vertices may be visited, far less
	// than the 15 vertices a global recomputation would touch.
	if m.Visited != 6 {
		t.Fatalf("local update visited %d vertices, want 6", m.Visited)
	}
	if m.Visited >= g.NumVertices() {
		t.Fatalf("local update visited %d of %d vertices: not local", m.Visited, g.NumVertices())
	}
	if m.Updated != 6 {
		t.Fatalf("updated %d vertices, want 6", m.Updated)
	}
	// Must match a full recomputation exactly.
	if want := CoreNumbers(g); !reflect.DeepEqual(m.Cores(), want) {
		t.Fatalf("incremental cores diverge from global recompute:\n got %v\nwant %v", m.Cores(), want)
	}
}

func TestIncrementalRemoveBridgeEdge(t *testing.T) {
	g := buildScenarioGraph()
	m := NewMaintainer(g)
	m.InsertEdge(12, 15)
	m.RemoveEdge(12, 15)

	if want := wantCores(); !reflect.DeepEqual(m.Cores(), want) {
		t.Fatalf("cores after remove mismatch:\n got %v\nwant %v", m.Cores(), want)
	}
	if m.Visited != 6 || m.Updated != 6 {
		t.Fatalf("remove visited=%d updated=%d, want 6/6", m.Visited, m.Updated)
	}
}

func TestIncrementalNoopGuards(t *testing.T) {
	g := buildScenarioGraph()
	m := NewMaintainer(g)
	before := m.Cores()

	m.InsertEdge(0, 1)  // duplicate edge
	m.InsertEdge(7, 7)  // self loop
	m.RemoveEdge(0, 30) // non-existent edge
	if !reflect.DeepEqual(m.Cores(), before) {
		t.Fatalf("no-op updates changed cores: %v", m.Cores())
	}
	if g.NumEdges() != buildScenarioGraph().NumEdges() {
		t.Fatalf("no-op updates changed edge count")
	}
}

// TestIncrementalAgainstGlobalRecompute is the strong correctness check:
// random edge insertions and deletions on sparse random graphs, with the
// maintainer state compared against a full recomputation after every op.
func TestIncrementalAgainstGlobalRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 5; trial++ {
		n := 30 + rng.Intn(30)
		g := NewGraph()
		for i := 0; i < n; i++ {
			g.AddVertex(i)
		}
		// Sparse random graph: ~2n edges.
		for e := 0; e < 2*n; e++ {
			g.AddEdge(rng.Intn(n), rng.Intn(n))
		}
		m := NewMaintainer(g)
		for op := 0; op < 200; op++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if u == v {
				continue
			}
			if g.HasEdge(u, v) && rng.Intn(2) == 0 {
				m.RemoveEdge(u, v)
			} else {
				m.InsertEdge(u, v)
			}
			want := CoreNumbers(g)
			if got := m.Cores(); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d op %d (%d,%d): divergence\n got %v\nwant %v",
					trial, op, u, v, got, want)
			}
		}
	}
}

// TestIncrementalLocalityOnLargerGraph checks that local updates stay
// local on a graph of many disjoint dense/pendant components: promoting
// one component must only visit that component, not the whole graph.
func TestIncrementalLocalityOnLargerGraph(t *testing.T) {
	const gadgets = 100 // 100 prisms x 6 vertices = 600 vertices
	g := NewGraph()
	for c := 0; c < gadgets; c++ {
		b := c * 10
		for _, e := range [][2]int{
			{0, 1}, {1, 2}, {0, 2},
			{3, 4}, {4, 5}, {3, 5},
			{0, 3}, {1, 4},
		} {
			g.AddEdge(b+e[0], b+e[1])
		}
	}
	m := NewMaintainer(g)
	totalVisited := 0
	for c := 0; c < gadgets; c++ {
		b := c * 10
		m.InsertEdge(b+2, b+5) // missing rung: promotes the prism 2 -> 3
		if m.Updated != 6 {
			t.Fatalf("gadget %d: updated %d vertices, want 6", c, m.Updated)
		}
		totalVisited += m.Visited
	}
	avg := float64(totalVisited) / gadgets
	t.Logf("avg visited per insert: %.1f of %d vertices", avg, g.NumVertices())
	if avg != 6 {
		t.Fatalf("local update visited avg %.1f vertices, want exactly 6 (one component)", avg)
	}
	if want := CoreNumbers(g); !reflect.DeepEqual(m.Cores(), want) {
		t.Fatalf("incremental cores diverge from global recompute")
	}
}
