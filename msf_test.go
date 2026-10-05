package main

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteForceMSF enumerates all 2^m edge subsets and returns the canonical
// minimum spanning forest: minimum total weight, ties broken by the
// lexicographically smallest edge list ordered by (weight, id) -- exactly the
// forest Kruskal produces under the same ordering.
func bruteForceMSF(n int, edges []Edge) (weight int, ids []int, comps int) {
	m := len(edges)
	byID := make(map[int]Edge, m)
	for _, e := range edges {
		byID[e.ID] = e
	}
	sortedKey := func(idList []int) []Edge {
		es := make([]Edge, len(idList))
		for i, id := range idList {
			es[i] = byID[id]
		}
		sort.Slice(es, func(i, j int) bool { return lessEdge(es[i], es[j]) })
		return es
	}
	lexLess := func(a, b []Edge) bool {
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i].W != b[i].W {
				return a[i].W < b[i].W
			}
			if a[i].ID != b[i].ID {
				return a[i].ID < b[i].ID
			}
		}
		return len(a) < len(b)
	}

	bestWeight := 0
	var bestIDs []int
	bestComps := 0
	found := false

	for mask := 0; mask < (1 << uint(m)); mask++ {
		d := newDSU(n)
		w := 0
		var ids []int
		acyclic := true
		for i := 0; i < m; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			e := edges[i]
			if e.U == e.V || !d.union(e.U, e.V) {
				acyclic = false
				break
			}
			w += e.W
			ids = append(ids, e.ID)
		}
		if !acyclic {
			continue
		}
		// A spanning forest must be maximal: every unused edge closes a cycle.
		maximal := true
		for i := 0; i < m; i++ {
			if mask&(1<<uint(i)) != 0 {
				continue
			}
			e := edges[i]
			if e.U != e.V && d.find(e.U) != d.find(e.V) {
				maximal = false
				break
			}
		}
		if !maximal {
			continue
		}
		c := 0
		for v := 0; v < n; v++ {
			if d.find(v) == v {
				c++
			}
		}
		if !found || w < bestWeight ||
			(w == bestWeight && lexLess(sortedKey(ids), sortedKey(bestIDs))) {
			found = true
			bestWeight = w
			bestIDs = append([]int(nil), ids...)
			bestComps = c
		}
	}
	return bestWeight, bestIDs, bestComps
}

func sortedInts(xs []int) []int {
	out := append([]int(nil), xs...)
	sort.Ints(out)
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkAgainstBruteForce validates the full analysis of one graph against
// brute-force enumeration, including every tree edge's replacement.
func checkAgainstBruteForce(t *testing.T, n int, edges []Edge) {
	t.Helper()
	got := Analyze(n, edges)

	bw, bids, bcomps := bruteForceMSF(n, edges)
	if got.Components != bcomps {
		t.Fatalf("n=%d edges=%v: components %d, brute force %d", n, edges, got.Components, bcomps)
	}
	if got.TotalWeight != bw {
		t.Fatalf("n=%d edges=%v: total weight %d, brute force %d", n, edges, got.TotalWeight, bw)
	}
	var gotIDs []int
	for _, e := range got.TreeEdges {
		gotIDs = append(gotIDs, e.ID)
	}
	if !equalInts(sortedInts(gotIDs), sortedInts(bids)) {
		t.Fatalf("n=%d edges=%v: tree edges %v, brute force %v", n, edges, sortedInts(gotIDs), sortedInts(bids))
	}

	inSet := func(xs []int, x int) bool {
		for _, v := range xs {
			if v == x {
				return true
			}
		}
		return false
	}

	for _, te := range got.TreeEdges {
		// Recompute the canonical MSF of the graph WITHOUT this tree edge.
		reduced := make([]Edge, 0, len(edges)-1)
		for _, e := range edges {
			if e.ID != te.ID {
				reduced = append(reduced, e)
			}
		}
		bw2, bids2, bcomps2 := bruteForceMSF(n, reduced)
		rep := got.Replacement[te.ID]

		if bcomps2 != bcomps {
			if rep != nil {
				t.Fatalf("n=%d edges=%v: tree edge %d marked replaceable by %d, but brute force says disconnected",
					n, edges, te.ID, rep.ID)
			}
			continue
		}
		// Same component count: canonical MSF(G-e) = MSF(G) - e + replacement.
		if rep == nil {
			t.Fatalf("n=%d edges=%v: tree edge %d marked disconnected, but brute force found replacement (weight delta %d)",
				n, edges, te.ID, bw2-bw+te.W)
		}
		var added []int
		for _, id := range bids2 {
			if !inSet(bids, id) {
				added = append(added, id)
			}
		}
		if len(added) != 1 || added[0] != rep.ID {
			t.Fatalf("n=%d edges=%v: tree edge %d replacement %d, brute force diff %v",
				n, edges, te.ID, rep.ID, added)
		}
		if bw2-bw+te.W != rep.W {
			t.Fatalf("n=%d edges=%v: tree edge %d replacement weight %d, brute force delta %d",
				n, edges, te.ID, rep.W, bw2-bw+te.W)
		}
	}
}

func TestFixedCases(t *testing.T) {
	// Triangle with tie broken by id: edges 1,2 weight 1; edge 3 weight 2.
	checkAgainstBruteForce(t, 3, []Edge{
		{ID: 1, U: 0, V: 1, W: 1},
		{ID: 2, U: 1, V: 2, W: 1},
		{ID: 3, U: 0, V: 2, W: 2},
	})
	// Parallel edges with equal weights: id decides; parallel edge is the
	// replacement of its twin.
	checkAgainstBruteForce(t, 2, []Edge{
		{ID: 10, U: 0, V: 1, W: 5},
		{ID: 11, U: 0, V: 1, W: 5},
		{ID: 12, U: 0, V: 1, W: 7},
	})
	// Disconnected graph with a bridge: no replacement exists.
	checkAgainstBruteForce(t, 4, []Edge{
		{ID: 1, U: 0, V: 1, W: 1},
		{ID: 2, U: 2, V: 3, W: 1},
	})
	// Self-loops are ignored everywhere.
	checkAgainstBruteForce(t, 3, []Edge{
		{ID: 1, U: 0, V: 0, W: 0},
		{ID: 2, U: 0, V: 1, W: 2},
		{ID: 3, U: 1, V: 2, W: 3},
		{ID: 4, U: 2, V: 2, W: 1},
	})
	// Negative weights.
	checkAgainstBruteForce(t, 4, []Edge{
		{ID: 1, U: 0, V: 1, W: -3},
		{ID: 2, U: 1, V: 2, W: -1},
		{ID: 3, U: 2, V: 3, W: 0},
		{ID: 4, U: 0, V: 3, W: 2},
		{ID: 5, U: 0, V: 2, W: -2},
	})
	// Single vertex, no edges.
	checkAgainstBruteForce(t, 1, nil)
}

func TestRandomSmallGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(7) // 1..7 vertices
		m := rng.Intn(17)    // 0..16 edges (2^16 subsets max)
		perm := rng.Perm(m)  // stable unique ids, shuffled
		edges := make([]Edge, 0, m)
		for i := 0; i < m; i++ {
			u := rng.Intn(n)
			v := rng.Intn(n)
			if rng.Intn(4) == 0 {
				v = u // occasional self-loop
			}
			edges = append(edges, Edge{
				ID: perm[i] + 1,
				U:  u,
				V:  v,
				W:  rng.Intn(11) - 3, // -3..7: many ties, some negative
			})
		}
		checkAgainstBruteForce(t, n, edges)
	}
}
