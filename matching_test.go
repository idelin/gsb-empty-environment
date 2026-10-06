package matching

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// bruteEssential enumerates all matchings by recursion over the left
// vertices and returns the maximum matching size together with the set of
// vertex pairs present in every maximum matching.
func bruteEssential(nL, nR int, pairs []pair) (int, map[pair]bool) {
	adj := make([][]int, nL)
	seen := make(map[pair]bool)
	for _, p := range pairs {
		if seen[p] {
			continue
		}
		seen[p] = true
		adj[p[0]] = append(adj[p[0]], p[1])
	}
	usedR := make([]bool, nR)
	var cur []pair
	best := -1
	var inter map[pair]bool
	var rec func(l int)
	rec = func(l int) {
		if l == nL {
			if len(cur) > best {
				best = len(cur)
				inter = make(map[pair]bool)
				for _, p := range cur {
					inter[p] = true
				}
			} else if len(cur) == best {
				for p := range inter {
					found := false
					for _, q := range cur {
						if q == p {
							found = true
							break
						}
					}
					if !found {
						delete(inter, p)
					}
				}
			}
			return
		}
		rec(l + 1) // leave l unmatched
		for _, r := range adj[l] {
			if !usedR[r] {
				usedR[r] = true
				cur = append(cur, pair{l, r})
				rec(l + 1)
				cur = cur[:len(cur)-1]
				usedR[r] = false
			}
		}
	}
	rec(0)
	return best, inter
}

func pairsOf(ids []int, edges []Edge) map[pair]bool {
	byID := make(map[int]pair)
	for _, e := range edges {
		byID[e.ID] = pair{e.Left, e.Right}
	}
	out := make(map[pair]bool)
	for _, id := range ids {
		out[byID[id]] = true
	}
	return out
}

// checkMatching verifies that ids form a valid matching of the graph.
func checkMatching(t *testing.T, ids []int, edges []Edge) {
	t.Helper()
	byID := make(map[int]pair)
	for _, e := range edges {
		byID[e.ID] = pair{e.Left, e.Right}
	}
	usedL := make(map[int]bool)
	usedR := make(map[int]bool)
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			t.Fatalf("matching contains unknown edge id %d", id)
		}
		if usedL[p[0]] || usedR[p[1]] {
			t.Fatalf("ids %v do not form a matching", ids)
		}
		usedL[p[0]] = true
		usedR[p[1]] = true
	}
}

func TestEmptyGraph(t *testing.T) {
	for _, dims := range [][2]int{{0, 0}, {3, 3}, {0, 5}, {5, 0}} {
		if got := MaximumMatching(dims[0], dims[1], nil); len(got) != 0 {
			t.Fatalf("MaximumMatching(%d, %d, nil) = %v, want empty", dims[0], dims[1], got)
		}
		if got := EssentialEdges(dims[0], dims[1], nil); len(got) != 0 {
			t.Fatalf("EssentialEdges(%d, %d, nil) = %v, want empty", dims[0], dims[1], got)
		}
	}
}

func TestAlternatingCycle(t *testing.T) {
	// K2,2: every matched edge lies on an alternating cycle, so no edge
	// is essential even though the matching is perfect.
	k22 := []Edge{{0, 0, 0}, {1, 0, 1}, {2, 1, 0}, {3, 1, 1}}
	if got := MaximumMatching(2, 2, k22); len(got) != 2 {
		t.Fatalf("MaximumMatching(K2,2) = %v, want size 2", got)
	}
	if got := EssentialEdges(2, 2, k22); len(got) != 0 {
		t.Fatalf("EssentialEdges(K2,2) = %v, want none", got)
	}

	// A 6-cycle has two perfect matchings; no edge is essential.
	c6 := []Edge{{0, 0, 0}, {1, 1, 1}, {2, 2, 2}, {3, 0, 1}, {4, 1, 2}, {5, 2, 0}}
	if got := EssentialEdges(3, 3, c6); len(got) != 0 {
		t.Fatalf("EssentialEdges(6-cycle) = %v, want none", got)
	}
}

func TestUnmatchedVertices(t *testing.T) {
	// Path L0-R0-L1-R1 plus isolated L2 and R2. The only maximum
	// matching is {(0,0), (1,1)}; both edges are essential.
	edges := []Edge{{0, 0, 0}, {1, 1, 0}, {2, 1, 1}}
	if got := EssentialEdges(3, 3, edges); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("EssentialEdges = %v, want [0 2]", got)
	}
}

func TestFreeVertexMakesEdgesAvoidable(t *testing.T) {
	// Chain on 3 left and 2 right vertices: maximum size 2, and every
	// matched edge is avoidable via an alternating path from a free
	// vertex, so nothing is essential.
	edges := []Edge{{0, 0, 0}, {1, 1, 0}, {2, 1, 1}, {3, 2, 1}}
	if got := MaximumMatching(3, 2, edges); len(got) != 2 {
		t.Fatalf("MaximumMatching = %v, want size 2", got)
	}
	if got := EssentialEdges(3, 2, edges); len(got) != 0 {
		t.Fatalf("EssentialEdges = %v, want none", got)
	}
}

func TestDuplicateEdges(t *testing.T) {
	// Same graph as TestUnmatchedVertices, but pair (0,0) is given twice
	// under ids 5 and 2. The matching reports the smallest id; the
	// essential pair reports all of its ids.
	edges := []Edge{{5, 0, 0}, {2, 0, 0}, {7, 1, 0}, {9, 1, 1}}
	if got := MaximumMatching(2, 2, edges); !reflect.DeepEqual(got, []int{2, 9}) {
		t.Fatalf("MaximumMatching = %v, want [2 9]", got)
	}
	if got := EssentialEdges(2, 2, edges); !reflect.DeepEqual(got, []int{2, 5, 9}) {
		t.Fatalf("EssentialEdges = %v, want [2 5 9]", got)
	}
}

func TestOutOfRangePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for out-of-range endpoint")
		}
	}()
	MaximumMatching(1, 1, []Edge{{0, 0, 1}})
}

// TestRandomAgainstBruteForce cross-checks the library against full
// enumeration of all matchings on random graphs of up to 8x8 vertices,
// including duplicate input edges with distinct ids.
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		nL := rng.Intn(9)
		nR := rng.Intn(9)
		p := 0.1 + 0.8*rng.Float64()
		var pairs []pair
		for l := 0; l < nL; l++ {
			for r := 0; r < nR; r++ {
				if rng.Float64() < p {
					pairs = append(pairs, pair{l, r})
				}
			}
		}
		// Build input edges: shuffled ids, plus random duplicates.
		var edges []Edge
		for _, p := range pairs {
			edges = append(edges, Edge{Left: p[0], Right: p[1]})
			for rng.Float64() < 0.25 {
				edges = append(edges, Edge{Left: p[0], Right: p[1]})
			}
		}
		perm := rng.Perm(len(edges))
		for i, id := range perm {
			edges[i].ID = id
		}

		best, wantEssential := bruteEssential(nL, nR, pairs)

		match := MaximumMatching(nL, nR, edges)
		if len(match) != best {
			t.Fatalf("iter %d: |MaximumMatching| = %d, want %d (nL=%d nR=%d pairs=%v)",
				iter, len(match), best, nL, nR, pairs)
		}
		checkMatching(t, match, edges)

		ess := EssentialEdges(nL, nR, edges)
		if !sort.IntsAreSorted(ess) {
			t.Fatalf("iter %d: EssentialEdges not sorted: %v", iter, ess)
		}
		gotEssential := pairsOf(ess, edges)
		if !reflect.DeepEqual(gotEssential, wantEssential) {
			t.Fatalf("iter %d: essential pairs = %v, want %v (nL=%d nR=%d pairs=%v)",
				iter, gotEssential, wantEssential, nL, nR, pairs)
		}

		// Essential pairs must appear in the returned maximum matching.
		matchPairs := pairsOf(match, edges)
		for p := range gotEssential {
			if !matchPairs[p] {
				t.Fatalf("iter %d: essential pair %v missing from matching %v",
					iter, p, match)
			}
		}
	}
}
