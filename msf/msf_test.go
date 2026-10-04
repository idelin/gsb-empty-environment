package main

import (
	"math/rand"
	"strconv"
	"testing"
)

// countComponents returns the number of connected components of (V, edges).
func countComponents(n int, edges []Edge) int {
	d := NewDSU(n)
	for _, e := range edges {
		if e.U != e.V {
			d.Union(e.U, e.V)
		}
	}
	roots := make(map[int]bool)
	for i := 0; i < n; i++ {
		roots[d.Find(i)] = true
	}
	return len(roots)
}

// bruteForceMSF enumerates every subset of edges and returns the minimum
// total weight over all maximal spanning forests of the graph. A subset is a
// spanning forest iff it is acyclic and has exactly n-components(G) edges.
func bruteForceMSF(n int, edges []Edge) int64 {
	var usable []Edge
	for _, e := range edges {
		if e.U != e.V {
			usable = append(usable, e)
		}
	}
	want := n - countComponents(n, edges) // edges in any maximal forest
	best := int64(1)<<62 - 1
	found := false
	m := len(usable)
	for mask := 0; mask < (1 << m); mask++ {
		if popcount(mask) != want {
			continue
		}
		d := NewDSU(n)
		ok := true
		var w int64
		for i := 0; i < m; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			w += usable[i].W
			if !d.Union(usable[i].U, usable[i].V) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if !found || w < best {
			best = w
			found = true
		}
	}
	if !found {
		panic("no spanning forest exists")
	}
	return best
}

func popcount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

// cutOf returns the vertex set reachable from start in the forest (treeSet
// edges of edges) after removing edge removedID.
func cutOf(n int, edges []Edge, treeSet map[int]bool, removedID, start int) []bool {
	adj := make([][]int, n)
	for _, e := range edges {
		if !treeSet[e.ID] || e.ID == removedID {
			continue
		}
		adj[e.U] = append(adj[e.U], e.V)
		adj[e.V] = append(adj[e.V], e.U)
	}
	in := make([]bool, n)
	in[start] = true
	stack := []int{start}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, v := range adj[u] {
			if !in[v] {
				in[v] = true
				stack = append(stack, v)
			}
		}
	}
	return in
}

// verifyGraph checks Analyze against full enumeration of spanning forests.
func verifyGraph(t *testing.T, n int, edges []Edge) {
	t.Helper()
	res := Analyze(n, edges)

	// 1. Total MSF weight must match the global enumeration minimum.
	if want := bruteForceMSF(n, edges); res.Total != want {
		t.Fatalf("n=%d edges=%v: total = %d, enumeration says %d", n, edges, res.Total, want)
	}

	compsG := countComponents(n, edges)
	treeSet := make(map[int]bool)
	for _, te := range res.TreeEdges {
		treeSet[te.ID] = true
	}
	if len(res.TreeEdges) != n-compsG {
		t.Fatalf("n=%d: MSF has %d edges, want %d", n, len(res.TreeEdges), n-compsG)
	}

	// 2. For every tree edge, validate the reported replacement globally.
	for _, te := range res.TreeEdges {
		var rest []Edge
		for _, e := range edges {
			if e.ID != te.ID {
				rest = append(rest, e)
			}
		}
		r := res.Replacement[te.ID]

		if countComponents(n, rest) > compsG {
			// Removing te disconnects the graph for good: no replacement.
			if r != nil {
				t.Errorf("edge %d: got replacement %d, want DISCONNECTED", te.ID, r.ID)
			}
			continue
		}
		if r == nil {
			t.Errorf("edge %d: got DISCONNECTED, but a replacement exists", te.ID)
			continue
		}

		// Weight consistency with the MSF of G \ {te}.
		wantTotal := bruteForceMSF(n, rest)
		if got := res.Total - te.W + r.W; got != wantTotal {
			t.Errorf("edge %d: replacement %d gives total %d, enumeration of G\\edge says %d",
				te.ID, r.ID, got, wantTotal)
		}

		// The replacement must cross the cut created by removing te, and be
		// the (weight, ID)-minimum among all crossing edges.
		cut := cutOf(n, edges, treeSet, te.ID, te.U)
		if cut[r.U] == cut[r.V] {
			t.Errorf("edge %d: replacement %d does not cross the cut", te.ID, r.ID)
		}
		var bestCross *Edge
		for _, e := range rest {
			if e.U == e.V || cut[e.U] == cut[e.V] {
				continue
			}
			if bestCross == nil || lessEdge(e, *bestCross) {
				cand := e
				bestCross = &cand
			}
		}
		if bestCross == nil || bestCross.ID != r.ID {
			want := "none"
			if bestCross != nil {
				want = strconv.Itoa(bestCross.ID)
			}
			t.Errorf("edge %d: replacement = %d, cheapest crossing edge = %s", te.ID, r.ID, want)
		}
	}
}

func TestTriangle(t *testing.T) {
	edges := []Edge{
		{ID: 10, U: 0, V: 1, W: 1},
		{ID: 11, U: 1, V: 2, W: 2},
		{ID: 12, U: 0, V: 2, W: 3},
	}
	res := Analyze(3, edges)
	if res.Total != 3 {
		t.Fatalf("total = %d, want 3", res.Total)
	}
	for _, id := range []int{10, 11} {
		r := res.Replacement[id]
		if r == nil || r.ID != 12 {
			t.Errorf("edge %d: replacement = %+v, want 12", id, r)
		}
	}
	verifyGraph(t, 3, edges)
}

func TestParallelEdgesTieByID(t *testing.T) {
	edges := []Edge{
		{ID: 3, U: 0, V: 1, W: 5},
		{ID: 1, U: 0, V: 1, W: 5},
		{ID: 2, U: 0, V: 1, W: 5},
		{ID: 4, U: 0, V: 1, W: 7},
	}
	res := Analyze(2, edges)
	if len(res.TreeEdges) != 1 || res.TreeEdges[0].ID != 1 {
		t.Fatalf("tree edges = %+v, want only id 1 (ID tie-break)", res.TreeEdges)
	}
	if r := res.Replacement[1]; r == nil || r.ID != 2 {
		t.Errorf("replacement = %+v, want id 2 (cheapest, ID tie-break over id 3)", r)
	}
	verifyGraph(t, 2, edges)
}

func TestDisconnected(t *testing.T) {
	edges := []Edge{
		{ID: 1, U: 0, V: 1, W: 4},
		{ID: 2, U: 2, V: 3, W: 6},
	}
	res := Analyze(4, edges)
	if res.Total != 10 {
		t.Fatalf("total = %d, want 10", res.Total)
	}
	for _, id := range []int{1, 2} {
		if res.Replacement[id] != nil {
			t.Errorf("edge %d: want DISCONNECTED, got %+v", id, res.Replacement[id])
		}
	}
	verifyGraph(t, 4, edges)
}

func TestDiamondPathCovering(t *testing.T) {
	// Path 0-1-2-3 (w=1 each) plus chords; replacements differ per tree edge.
	edges := []Edge{
		{ID: 1, U: 0, V: 1, W: 1},
		{ID: 2, U: 1, V: 2, W: 1},
		{ID: 3, U: 2, V: 3, W: 1},
		{ID: 4, U: 0, V: 3, W: 10},
		{ID: 5, U: 0, V: 2, W: 5},
		{ID: 6, U: 1, V: 3, W: 6},
	}
	res := Analyze(4, edges)
	want := map[int]int{1: 5, 2: 5, 3: 6}
	for id, wantR := range want {
		r := res.Replacement[id]
		if r == nil || r.ID != wantR {
			t.Errorf("edge %d: replacement = %+v, want %d", id, r, wantR)
		}
	}
	verifyGraph(t, 4, edges)
}

func TestSelfLoopsAndIsolated(t *testing.T) {
	edges := []Edge{
		{ID: 1, U: 0, V: 0, W: -100}, // self-loop: never usable
		{ID: 2, U: 0, V: 1, W: 3},
	}
	res := Analyze(3, edges)
	if res.Total != 3 || len(res.TreeEdges) != 1 {
		t.Fatalf("total=%d treeEdges=%+v", res.Total, res.TreeEdges)
	}
	if res.Replacement[2] != nil {
		t.Errorf("edge 2: want DISCONNECTED")
	}
	verifyGraph(t, 3, edges)
}

// TestRandomAgainstEnumeration generates many small random graphs (with
// parallel edges, self-loops, negative weights, disconnected graphs, and
// shuffled non-contiguous IDs) and validates against full enumeration.
func TestRandomAgainstEnumeration(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(8)  // 1..8 vertices
		m := rng.Intn(17)     // 0..16 edges (2^16 subsets max)
		ids := rng.Perm(m)
		edges := make([]Edge, m)
		for i := range edges {
			edges[i] = Edge{
				ID: ids[i] * 7, // sparse, shuffled, stable IDs
				U:  rng.Intn(n),
				V:  rng.Intn(n),
				W:  int64(rng.Intn(11) - 4), // -4..6: forces many ties
			}
		}
		verifyGraph(t, n, edges)
	}
}
