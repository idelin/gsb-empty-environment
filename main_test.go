package main

import (
	"math/rand"
	"strings"
	"testing"
)

func enumerateAllCuts(t *testing.T, g *Graph) (int64, [][]int) {
	t.Helper()
	maxFlow, residual := maxFlowValue(g)
	enum := NewEnumerator(residual, g.N, g.S, g.T)
	var cuts [][]int
	enum.Visit(func(cut []int) bool {
		cuts = append(cuts, cut)
		return true
	})
	return maxFlow, cuts
}

func randomGraph(rng *rand.Rand, n, m, maxCap int) *Graph {
	g := &Graph{N: n, S: 0, T: n - 1}
	for i := 0; i < m; i++ {
		g.Edges = append(g.Edges, Edge{
			U: rng.Intn(n),
			V: rng.Intn(n),
			C: int64(rng.Intn(maxCap + 1)),
		})
	}
	return g
}

// The enumerator must agree with exhaustive subset enumeration on value,
// content, and order, and every emitted cut must independently have
// capacity equal to the max-flow value.
func TestRandomGraphsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		n := 2 + rng.Intn(7) // 2..8 vertices
		m := rng.Intn(2*n + 1)
		g := randomGraph(rng, n, m, 4)

		maxFlow, cuts := enumerateAllCuts(t, g)
		best, bruteCuts := bruteForceMinCuts(g)

		if best != maxFlow {
			t.Fatalf("iter %d: exhaustive min-cut %d != max-flow %d (graph %+v)",
				iter, best, maxFlow, g)
		}
		if !equalCuts(cuts, bruteCuts) {
			t.Fatalf("iter %d: enumerator cuts %v != brute-force cuts %v (graph %+v)",
				iter, cuts, bruteCuts, g)
		}
		for _, cut := range cuts {
			if cap := cutCapacity(g, cut); cap != maxFlow {
				t.Fatalf("iter %d: cut %v has capacity %d != %d", iter, cut, cap, maxFlow)
			}
		}
		for i := 1; i < len(cuts); i++ {
			if lexCompare(cuts[i-1], cuts[i]) >= 0 {
				t.Fatalf("iter %d: cuts not strictly increasing at %d: %v", iter, i, cuts)
			}
		}
	}
}

// A diamond with a cross edge: the same min cuts are reachable through
// many different residual paths; each must still appear exactly once.
// Min cuts of value 2: {1}, {1,3}, {1,2,3} ({1,2} has capacity 3).
func TestDiamondNoDuplicates(t *testing.T) {
	g := &Graph{N: 4, S: 0, T: 3, Edges: []Edge{
		{0, 1, 1}, {0, 2, 1}, {1, 3, 1}, {2, 3, 1}, {1, 2, 1},
	}}
	maxFlow, cuts := enumerateAllCuts(t, g)
	if maxFlow != 2 {
		t.Fatalf("max flow = %d, want 2", maxFlow)
	}
	want := [][]int{{1}, {1, 2, 3}, {1, 3}}
	if !equalCuts(cuts, want) {
		t.Fatalf("cuts = %v, want %v", cuts, want)
	}
}

// Zero max flow: min cuts are all closed sets containing s and not t;
// here only {1,2} has no outgoing arc.
func TestDisconnectedSink(t *testing.T) {
	g := &Graph{N: 3, S: 0, T: 2, Edges: []Edge{{0, 1, 5}}}
	maxFlow, cuts := enumerateAllCuts(t, g)
	if maxFlow != 0 {
		t.Fatalf("max flow = %d, want 0", maxFlow)
	}
	want := [][]int{{1, 2}}
	if !equalCuts(cuts, want) {
		t.Fatalf("cuts = %v, want %v", cuts, want)
	}
}

func collectPages(t *testing.T, g *Graph, limit int) ([][]int, []bool) {
	t.Helper()
	var all [][]int
	var completes []bool
	cursor := ""
	for page := 0; ; page++ {
		if page > 64 {
			t.Fatal("pagination did not terminate")
		}
		out, err := computePage(g, limit, cursor)
		if err != nil {
			t.Fatalf("computePage: %v", err)
		}
		if !out.Verified {
			t.Fatal("page not verified")
		}
		all = append(all, out.Cuts...)
		completes = append(completes, out.Complete)
		if out.Complete {
			if out.NextCursor != "" {
				t.Fatal("complete page must not carry a cursor")
			}
			return all, completes
		}
		if out.NextCursor == "" {
			t.Fatal("incomplete page must carry a cursor")
		}
		cursor = out.NextCursor
	}
}

func TestPaginationReassemblesFullList(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 60; iter++ {
		n := 2 + rng.Intn(6)
		g := randomGraph(rng, n, 2+rng.Intn(2*n), 3)
		_, want := enumerateAllCuts(t, g)
		for _, limit := range []int{1, 2, 3, 5} {
			got, completes := collectPages(t, g, limit)
			if !equalCuts(got, want) {
				t.Fatalf("iter %d limit %d: paged cuts %v != full %v", iter, limit, got, want)
			}
			for i, c := range completes {
				if c && i != len(completes)-1 {
					t.Fatalf("iter %d limit %d: non-final page claims complete", iter, limit)
				}
			}
			if !completes[len(completes)-1] {
				t.Fatalf("iter %d limit %d: final page not complete", iter, limit)
			}
		}
	}
}

func TestCursorBoundToGraphVersion(t *testing.T) {
	g := &Graph{N: 4, S: 0, T: 3, Edges: []Edge{
		{0, 1, 1}, {0, 2, 1}, {1, 3, 1}, {2, 3, 1},
	}}
	first, err := computePage(g, 1, "")
	if err != nil {
		t.Fatalf("computePage: %v", err)
	}
	if first.Complete || first.NextCursor == "" {
		t.Fatalf("expected incomplete first page with cursor, got %+v", first)
	}
	// Same graph: cursor accepted.
	if _, err := computePage(g, 1, first.NextCursor); err != nil {
		t.Fatalf("cursor rejected by issuing graph: %v", err)
	}
	// Changed capacity: cursor must be rejected.
	g2 := &Graph{N: 4, S: 0, T: 3, Edges: []Edge{
		{0, 1, 2}, {0, 2, 1}, {1, 3, 1}, {2, 3, 1},
	}}
	if _, err := computePage(g2, 1, first.NextCursor); err == nil {
		t.Fatal("cursor accepted for a modified graph")
	} else if !strings.Contains(err.Error(), "graph version") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tampered cursor must be rejected.
	if _, err := computePage(g, 1, first.NextCursor[:len(first.NextCursor)-2]+"xx"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestSelfCheckConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 50; iter++ {
		n := 2 + rng.Intn(6)
		g := randomGraph(rng, n, rng.Intn(2*n+1), 4)
		maxFlow, _ := enumerateAllCuts(t, g)
		report, err := runSelfCheck(g, maxFlow)
		if err != nil {
			t.Fatalf("iter %d: self-check error: %v", iter, err)
		}
		if !report.OK {
			t.Fatalf("iter %d: self-check not ok: %+v", iter, report)
		}
	}
}

func TestParseGraphValidation(t *testing.T) {
	if _, err := ParseGraph(strings.NewReader("4 4 1 4\n1 2 1\n1 3 1\n2 4 1\n3 4 1\n")); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	bad := []string{
		"1 0 1 1\n",         // n < 2
		"2 0 1 1\n",         // s == t
		"2 1 1 2\n1 2 -3\n", // negative capacity
		"2 1 1 2\n1 9 3\n",  // endpoint out of range
		"2 1 1 2\n1 2\n",    // truncated edge
	}
	for _, in := range bad {
		if _, err := ParseGraph(strings.NewReader(in)); err == nil {
			t.Fatalf("invalid input %q accepted", in)
		}
	}
}

func TestGraphVersionChanges(t *testing.T) {
	base := &Graph{N: 2, S: 0, T: 1, Edges: []Edge{{0, 1, 1}}}
	same := &Graph{N: 2, S: 0, T: 1, Edges: []Edge{{0, 1, 1}}}
	diffCap := &Graph{N: 2, S: 0, T: 1, Edges: []Edge{{0, 1, 2}}}
	diffEdge := &Graph{N: 2, S: 0, T: 1, Edges: []Edge{{0, 1, 1}, {0, 1, 1}}}
	if base.Version() != same.Version() {
		t.Fatal("identical graphs have different versions")
	}
	if base.Version() == diffCap.Version() || base.Version() == diffEdge.Version() {
		t.Fatal("different graphs share a version")
	}
}
