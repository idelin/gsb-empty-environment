package dagpath

import (
	"math/rand"
	"strings"
	"testing"
)

func mustGraph(t *testing.T, numNodes int, edges []Edge) *Graph {
	t.Helper()
	g, err := NewGraph(numNodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func mustPath(t *testing.T, g *Graph, s, tgt int) Path {
	t.Helper()
	p, found, err := g.BestPath(s, tgt)
	if err != nil {
		t.Fatalf("BestPath: %v", err)
	}
	if !found {
		t.Fatalf("BestPath: no path %d -> %d", s, tgt)
	}
	return p
}

// TestNotGreedyByEdgeLabel proves per-edge label ordering cannot replace
// whole-string comparison: locally smallest label "a" leads to "az", while
// the locally larger "ab" wins globally.
func TestNotGreedyByEdgeLabel(t *testing.T) {
	g := mustGraph(t, 3, []Edge{
		{ID: 0, From: 0, To: 1, Label: "a"},
		{ID: 1, From: 1, To: 2, Label: "z"},
		{ID: 2, From: 0, To: 2, Label: "ab"},
	})
	p := mustPath(t, g, 0, 2)
	if got := p.String(); got != "ab" {
		t.Fatalf("string = %q, want %q", got, "ab")
	}
	if ids := p.EdgeIDs(); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("edge IDs = %v, want [2]", ids)
	}
}

func TestEmptyFragments(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 0, From: 0, To: 1, Label: ""},
		{ID: 1, From: 1, To: 3, Label: "a"},
		{ID: 2, From: 0, To: 2, Label: ""},
		{ID: 3, From: 2, To: 3, Label: ""}, // path 0-2-3 gives "" with 2 edges
		{ID: 4, From: 0, To: 3, Label: ""}, // path 0-3 gives "" with 1 edge
	})
	p := mustPath(t, g, 0, 3)
	if got := p.String(); got != "" {
		t.Fatalf("string = %q, want empty", got)
	}
	if ids := p.EdgeIDs(); len(ids) != 1 || ids[0] != 4 {
		t.Fatalf("edge IDs = %v, want [4] (fewest edges among empty strings)", ids)
	}
}

func TestTieBreakEdgeCount(t *testing.T) {
	g := mustGraph(t, 3, []Edge{
		{ID: 0, From: 0, To: 1, Label: "a"},
		{ID: 1, From: 1, To: 2, Label: "b"},
		{ID: 2, From: 0, To: 2, Label: "ab"},
	})
	p := mustPath(t, g, 0, 2)
	if ids := p.EdgeIDs(); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("edge IDs = %v, want [2] (same string %q, fewer edges)", ids, "ab")
	}
}

func TestTieBreakEdgeIDs(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 5, From: 0, To: 1, Label: "a"},
		{ID: 9, From: 1, To: 3, Label: "b"},
		{ID: 2, From: 0, To: 2, Label: "a"},
		{ID: 7, From: 2, To: 3, Label: "b"},
	})
	p := mustPath(t, g, 0, 3)
	if got := p.String(); got != "ab" {
		t.Fatalf("string = %q, want %q", got, "ab")
	}
	if ids := p.EdgeIDs(); len(ids) != 2 || ids[0] != 2 || ids[1] != 7 {
		t.Fatalf("edge IDs = %v, want [2 7]", ids)
	}
}

// TestSharedSuffixDiamond stacks k binary-choice layers over one huge shared
// suffix edge. There are 2^k distinct paths; the DP must share the suffix
// rope instead of re-concatenating it per path.
func TestSharedSuffixDiamond(t *testing.T) {
	const layers = 24
	big := strings.Repeat("q", 200000)
	edges := make([]Edge, 0, 2*layers+1)
	for i := 0; i < layers; i++ {
		edges = append(edges,
			Edge{ID: 2 * i, From: i, To: i + 1, Label: "az"},
			Edge{ID: 2*i + 1, From: i, To: i + 1, Label: "b"},
		)
	}
	edges = append(edges, Edge{ID: 2 * layers, From: layers, To: layers + 1, Label: big})
	g := mustGraph(t, layers+2, edges)

	p := mustPath(t, g, 0, layers+1)
	want := strings.Repeat("az", layers) + big // "az" < "b" at every layer
	if got := p.String(); got != want {
		t.Fatalf("string mismatch: got len %d, want len %d", len(got), len(want))
	}
	if len(p.Edges) != layers+1 {
		t.Fatalf("edge count = %d, want %d", len(p.Edges), layers+1)
	}
}

func TestUnreachable(t *testing.T) {
	g := mustGraph(t, 3, []Edge{{ID: 0, From: 0, To: 1, Label: "a"}})
	if _, found, err := g.BestPath(1, 2); err != nil || found {
		t.Fatalf("found=%v err=%v, want found=false err=nil", found, err)
	}
}

func TestSelfPath(t *testing.T) {
	g := mustGraph(t, 2, []Edge{{ID: 0, From: 0, To: 1, Label: "a"}})
	p := mustPath(t, g, 1, 1)
	if got := p.String(); got != "" || len(p.Edges) != 0 {
		t.Fatalf("trivial path = %q/%d edges, want empty", got, len(p.Edges))
	}
}

func TestCycleRejected(t *testing.T) {
	_, err := NewGraph(2, []Edge{
		{ID: 0, From: 0, To: 1},
		{ID: 1, From: 1, To: 0},
	})
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

// --- brute-force cross-check ---------------------------------------------

// bruteForce enumerates every s-t path and returns the best one under
// (concatenated string, edge count, edge-ID sequence).
func bruteForce(g *Graph, s, tgt int) (bestStr string, bestIDs []int, found bool) {
	var bestPath []int
	var cur []int
	var dfs func(u int)
	dfs = func(u int) {
		if u == tgt {
			if bestPath == nil || pathLess(g, cur, bestPath) {
				bestPath = append([]int(nil), cur...)
			}
			return
		}
		for _, ei := range g.out[u] {
			cur = append(cur, ei)
			dfs(g.Edges[ei].To)
			cur = cur[:len(cur)-1]
		}
	}
	dfs(s)
	if bestPath == nil {
		return "", nil, false
	}
	var sb strings.Builder
	ids := make([]int, len(bestPath))
	for i, ei := range bestPath {
		sb.WriteString(g.Edges[ei].Label)
		ids[i] = g.Edges[ei].ID
	}
	return sb.String(), ids, true
}

func pathLess(g *Graph, a, b []int) bool {
	var sa, sb strings.Builder
	for _, ei := range a {
		sa.WriteString(g.Edges[ei].Label)
	}
	for _, ei := range b {
		sb.WriteString(g.Edges[ei].Label)
	}
	if sa.String() != sb.String() {
		return sa.String() < sb.String()
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if g.Edges[a[i]].ID != g.Edges[b[i]].ID {
			return g.Edges[a[i]].ID < g.Edges[b[i]].ID
		}
	}
	return false
}

// TestAgainstBruteForce generates random small DAGs (edges only i->j with
// i<j), including empty fragments and prefix-related labels, and checks the
// solver against full path enumeration.
func TestAgainstBruteForce(t *testing.T) {
	labels := []string{"", "a", "b", "c", "aa", "ab", "ba", "abc", "aab", "bca"}
	for seed := int64(0); seed < 400; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 2 + r.Intn(7)
		var edges []Edge
		id := 0
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				for r.Float64() < 0.35 {
					edges = append(edges, Edge{
						ID:    id,
						From:  i,
						To:    j,
						Label: labels[r.Intn(len(labels))],
					})
					id++
				}
			}
		}
		// Shuffle edge order so IDs are not positional.
		r.Shuffle(len(edges), func(a, b int) { edges[a], edges[b] = edges[b], edges[a] })

		g := mustGraph(t, n, edges)
		s, tgt := 0, n-1
		wantStr, wantIDs, wantFound := bruteForce(g, s, tgt)

		p, found, err := g.BestPath(s, tgt)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if found != wantFound {
			t.Fatalf("seed %d: found=%v, want %v", seed, found, wantFound)
		}
		if !found {
			continue
		}
		if got := p.String(); got != wantStr {
			t.Fatalf("seed %d: string = %q, want %q", seed, got, wantStr)
		}
		if got := p.EdgeIDs(); !equalInts(got, wantIDs) {
			t.Fatalf("seed %d: edge IDs = %v, want %v", seed, got, wantIDs)
		}
	}
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
