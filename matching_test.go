package matching

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// bruteForce enumerates every matching of the pair set and returns the
// maximum matching size together with the set of pairs that appear in all
// maximum matchings. Used as an oracle for graphs up to 8x8.
func bruteForce(n, m int, pairs [][2]int) (int, map[[2]int]bool) {
	adj := make([][]int, n)
	seen := make(map[[2]int]bool)
	for _, p := range pairs {
		if seen[p] {
			continue
		}
		seen[p] = true
		adj[p[0]] = append(adj[p[0]], p[1])
	}

	best := -1
	var intersection map[[2]int]bool
	usedR := make([]bool, m)
	current := make([][2]int, 0, n)

	var rec func(u int)
	rec = func(u int) {
		if u == n {
			if len(current) > best {
				best = len(current)
				intersection = make(map[[2]int]bool, len(current))
				for _, p := range current {
					intersection[p] = true
				}
			} else if len(current) == best {
				inCur := make(map[[2]int]bool, len(current))
				for _, p := range current {
					inCur[p] = true
				}
				for p := range intersection {
					if !inCur[p] {
						delete(intersection, p)
					}
				}
			}
			return
		}
		rec(u + 1) // leave u unmatched
		for _, v := range adj[u] {
			if !usedR[v] {
				usedR[v] = true
				current = append(current, [2]int{u, v})
				rec(u + 1)
				current = current[:len(current)-1]
				usedR[v] = false
			}
		}
	}
	rec(0)
	return best, intersection
}

func pairSet(edges []Edge) map[[2]int]bool {
	out := make(map[[2]int]bool, len(edges))
	for _, e := range edges {
		out[[2]int{e.Left, e.Right}] = true
	}
	return out
}

func sortedPairs(set map[[2]int]bool) [][2]int {
	out := make([][2]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// TestAgainstBruteForce cross-checks MaxMatching and EssentialEdges against
// full enumeration of all matchings on random graphs up to 8x8, including
// duplicate input edges.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		n := rng.Intn(9)
		m := rng.Intn(9)
		density := 0.1 + 0.8*rng.Float64()
		g := NewGraph(n, m)
		var pairs [][2]int
		for u := 0; u < n; u++ {
			for v := 0; v < m; v++ {
				if rng.Float64() < density {
					g.AddEdge(u, v)
					pairs = append(pairs, [2]int{u, v})
					// Randomly duplicate the edge with a fresh id.
					for rng.Float64() < 0.2 {
						g.AddEdge(u, v)
					}
				}
			}
		}

		wantSize, wantEssential := bruteForce(n, m, pairs)

		match := g.MaxMatching()
		if len(match) != wantSize {
			t.Fatalf("trial %d (%dx%d, %d pairs): MaxMatching size = %d, want %d",
				trial, n, m, len(pairs), len(match), wantSize)
		}
		// The returned matching must be a valid matching.
		usedL := make(map[int]bool)
		usedR := make(map[int]bool)
		for _, e := range match {
			if usedL[e.Left] || usedR[e.Right] {
				t.Fatalf("trial %d: MaxMatching reuses a vertex: %+v", trial, match)
			}
			usedL[e.Left] = true
			usedR[e.Right] = true
		}

		gotEssential := pairSet(g.EssentialEdges())
		if !reflect.DeepEqual(sortedPairs(gotEssential), sortedPairs(wantEssential)) {
			t.Fatalf("trial %d (%dx%d, pairs %v): EssentialEdges = %v, want %v",
				trial, n, m, pairs, sortedPairs(gotEssential), sortedPairs(wantEssential))
		}
		// Essential edges must be part of the returned maximum matching.
		matchPairs := pairSet(match)
		for p := range gotEssential {
			if !matchPairs[p] {
				t.Fatalf("trial %d: essential pair %v not in MaxMatching %v",
					trial, p, sortedPairs(matchPairs))
			}
		}
	}
}

func essentialPairs(g *Graph) [][2]int {
	return sortedPairs(pairSet(g.EssentialEdges()))
}

func TestEmptyGraph(t *testing.T) {
	for _, sizes := range [][2]int{{0, 0}, {0, 5}, {5, 0}, {3, 4}} {
		g := NewGraph(sizes[0], sizes[1])
		if got := g.MaxMatching(); len(got) != 0 {
			t.Errorf("%v: MaxMatching = %v, want empty", sizes, got)
		}
		if got := g.EssentialEdges(); len(got) != 0 {
			t.Errorf("%v: EssentialEdges = %v, want empty", sizes, got)
		}
	}
}

func TestAlternatingCycle(t *testing.T) {
	// K2,2: two maximum matchings, no edge is in all of them.
	g := NewGraph(2, 2)
	g.AddEdge(0, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 0)
	g.AddEdge(1, 1)
	if got := len(g.MaxMatching()); got != 2 {
		t.Errorf("MaxMatching size = %d, want 2", got)
	}
	if got := essentialPairs(g); len(got) != 0 {
		t.Errorf("EssentialEdges = %v, want none", got)
	}
}

func TestCycleWithEssentialTail(t *testing.T) {
	// A 4-cycle on {0,1}x{0,1} plus a forced tail edge (2,2).
	g := NewGraph(3, 3)
	g.AddEdge(0, 0)
	g.AddEdge(0, 1)
	g.AddEdge(1, 0)
	g.AddEdge(1, 1)
	g.AddEdge(2, 2)
	want := [][2]int{{2, 2}}
	if got := essentialPairs(g); !reflect.DeepEqual(got, want) {
		t.Errorf("EssentialEdges = %v, want %v", got, want)
	}
}

func TestUnmatchedVertices(t *testing.T) {
	// Four left vertices, only two can ever be matched.
	g := NewGraph(4, 2)
	g.AddEdge(0, 0)
	g.AddEdge(1, 1)
	if got := len(g.MaxMatching()); got != 2 {
		t.Errorf("MaxMatching size = %d, want 2", got)
	}
	want := [][2]int{{0, 0}, {1, 1}}
	if got := essentialPairs(g); !reflect.DeepEqual(got, want) {
		t.Errorf("EssentialEdges = %v, want %v", got, want)
	}
}

func TestFreeVertexAlternatingPath(t *testing.T) {
	// Two left vertices compete for one right vertex: either can be
	// matched, so nothing is essential.
	g := NewGraph(2, 1)
	g.AddEdge(0, 0)
	g.AddEdge(1, 0)
	if got := len(g.MaxMatching()); got != 1 {
		t.Errorf("MaxMatching size = %d, want 1", got)
	}
	if got := essentialPairs(g); len(got) != 0 {
		t.Errorf("EssentialEdges = %v, want none", got)
	}
}

func TestUniquePerfectMatching(t *testing.T) {
	// Path graph with a unique maximum matching: every matched edge is
	// essential.
	g := NewGraph(2, 2)
	g.AddEdge(0, 0)
	g.AddEdge(1, 0)
	g.AddEdge(1, 1)
	want := [][2]int{{0, 0}, {1, 1}}
	if got := essentialPairs(g); !reflect.DeepEqual(got, want) {
		t.Errorf("EssentialEdges = %v, want %v", got, want)
	}
}

func TestDuplicateEdges(t *testing.T) {
	// Duplicate edges get distinct ids but share pair semantics.
	g := NewGraph(2, 3)
	id00a := g.AddEdge(0, 0)
	g.AddEdge(0, 0) // duplicate of pair (0,0)
	g.AddEdge(0, 2)
	id11 := g.AddEdge(1, 1)
	g.AddEdge(1, 1) // duplicate of pair (1,1)

	// Max matchings: {(0,0),(1,1)} and {(0,2),(1,1)}: only (1,1) is essential.
	essential := g.EssentialEdges()
	if len(essential) != 1 || essential[0].Left != 1 || essential[0].Right != 1 {
		t.Fatalf("EssentialEdges = %+v, want pair (1,1)", essential)
	}
	if essential[0].ID != id11 {
		t.Errorf("essential edge id = %d, want canonical id %d", essential[0].ID, id11)
	}

	// The matching reports canonical ids of matched pairs.
	for _, e := range g.MaxMatching() {
		if e.Left == 0 && e.Right == 0 && e.ID != id00a {
			t.Errorf("matched pair (0,0) has id %d, want canonical id %d", e.ID, id00a)
		}
	}
}

func TestSingleEdge(t *testing.T) {
	g := NewGraph(3, 3)
	id := g.AddEdge(2, 1)
	essential := g.EssentialEdges()
	if len(essential) != 1 || essential[0].ID != id {
		t.Errorf("EssentialEdges = %+v, want the single edge", essential)
	}
}

func TestComplete8x8(t *testing.T) {
	// K8,8: every perfect matching is maximum, so no edge is essential.
	g := NewGraph(8, 8)
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			g.AddEdge(u, v)
		}
	}
	if got := len(g.MaxMatching()); got != 8 {
		t.Errorf("MaxMatching size = %d, want 8", got)
	}
	if got := essentialPairs(g); len(got) != 0 {
		t.Errorf("EssentialEdges = %v, want none", got)
	}
}
