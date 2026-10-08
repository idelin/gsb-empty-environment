package graph

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates all simple s-t paths (sufficient for non-negative
// weights, even with zero-weight cycles) and returns the minimum total
// weight, ignoring the edge with ID banned.
func bruteForce(g *Graph, s, t, banned int) (best int64, reachable bool) {
	visited := make([]bool, g.NumNodes)
	var dfs func(u int, acc int64)
	dfs = func(u int, acc int64) {
		if u == t {
			if !reachable || acc < best {
				best, reachable = acc, true
			}
			return
		}
		visited[u] = true
		for _, id := range g.adj[u] {
			if id == banned {
				continue
			}
			e := g.Edges[id]
			if !visited[e.To] {
				dfs(e.To, acc+e.Weight)
			}
		}
		visited[u] = false
	}
	dfs(s, 0)
	return best, reachable
}

// checkReplacements verifies every replacement result against the
// brute-force distance and validates the witness path.
func checkReplacements(t *testing.T, g *Graph, s, tgt int, path []int, got []Replacement) {
	t.Helper()
	if len(got) != len(path) {
		t.Fatalf("got %d replacements, want %d", len(got), len(path))
	}
	for i, r := range got {
		if r.RemovedEdge != path[i] {
			t.Fatalf("result %d removes edge %d, want %d", i, r.RemovedEdge, path[i])
		}
		wantDist, wantOK := bruteForce(g, s, tgt, r.RemovedEdge)
		if r.Reachable != wantOK {
			t.Fatalf("remove edge %d: reachable=%v, brute force=%v", r.RemovedEdge, r.Reachable, wantOK)
		}
		if !r.Reachable {
			continue
		}
		if r.Dist != wantDist {
			t.Fatalf("remove edge %d: dist=%d, brute force=%d", r.RemovedEdge, r.Dist, wantDist)
		}
		// The witness must be a valid s-t path of matching weight that
		// does not use the removed edge.
		for _, id := range r.Witness {
			if id == r.RemovedEdge {
				t.Fatalf("remove edge %d: witness uses the removed edge", r.RemovedEdge)
			}
		}
		w, ok := g.PathWeight(s, tgt, r.Witness)
		if !ok {
			t.Fatalf("remove edge %d: witness %v is not a valid %d->%d path", r.RemovedEdge, r.Witness, s, tgt)
		}
		if w != r.Dist {
			t.Fatalf("remove edge %d: witness weight %d != dist %d", r.RemovedEdge, w, r.Dist)
		}
	}
}

func shortestPathOrFail(t *testing.T, g *Graph, s, tgt int) []int {
	t.Helper()
	res := g.ShortestPath(s, tgt, NoEdge)
	if !res.Reachable {
		t.Fatalf("%d unreachable from %d", tgt, s)
	}
	return res.Path
}

func TestReplacementParallelEdges(t *testing.T) {
	// Two parallel s->t arcs; the shortest path uses the cheap one, and
	// removing it must fall back to the parallel arc or the detour.
	g := New(3)              // 0=s, 1=a, 2=t
	e0 := g.AddEdge(0, 2, 5) // parallel, cheap
	g.AddEdge(0, 2, 7)       // parallel, expensive
	e2 := g.AddEdge(0, 1, 3)
	e3 := g.AddEdge(1, 2, 3)

	path := shortestPathOrFail(t, g, 0, 2)
	if len(path) != 1 || path[0] != e0 {
		t.Fatalf("shortest path = %v, want [%d]", path, e0)
	}
	got, err := g.ReplacementPaths(0, 2, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Reachable || got[0].Dist != 6 {
		t.Fatalf("remove parallel edge: got %+v, want dist 6 via detour", got)
	}
	w, ok := g.PathWeight(0, 2, got[0].Witness)
	if !ok || w != 6 {
		t.Fatalf("witness %v invalid", got[0].Witness)
	}
	_ = e2
	_ = e3
}

func TestReplacementZeroWeightCycle(t *testing.T) {
	// Zero-weight cycle a<->b on the detour; Dijkstra and the brute force
	// must agree that the cycle contributes nothing.
	g := New(4) // 0=s, 1=a, 2=b, 3=t
	direct := g.AddEdge(0, 3, 2)
	g.AddEdge(0, 1, 0)
	g.AddEdge(1, 2, 0)
	g.AddEdge(2, 1, 0) // zero-weight cycle
	g.AddEdge(1, 3, 3)

	path := shortestPathOrFail(t, g, 0, 3)
	if len(path) != 1 || path[0] != direct {
		t.Fatalf("shortest path = %v, want [%d]", path, direct)
	}
	got, err := g.ReplacementPaths(0, 3, path)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].Reachable || got[0].Dist != 3 {
		t.Fatalf("got %+v, want dist 3 through the zero-weight cycle", got[0])
	}
	checkReplacements(t, g, 0, 3, path, got)
}

func TestReplacementUnreachable(t *testing.T) {
	// Single bridge route: removing either path edge disconnects t.
	g := New(3)
	g.AddEdge(0, 1, 1)
	g.AddEdge(1, 2, 1)

	path := shortestPathOrFail(t, g, 0, 2)
	got, err := g.ReplacementPaths(0, 2, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	for _, r := range got {
		if r.Reachable {
			t.Fatalf("remove edge %d: expected explicit unreachable marker, got dist %d", r.RemovedEdge, r.Dist)
		}
	}
	checkReplacements(t, g, 0, 2, path, got)
}

func TestReplacementEqualShortestPaths(t *testing.T) {
	// Three equally short routes; removing the reference path's edge must
	// keep the distance at 4 via one of the other routes.
	g := New(4) // 0=s, 1=a, 2=b, 3=t
	direct := g.AddEdge(0, 3, 4)
	g.AddEdge(0, 1, 2)
	g.AddEdge(1, 3, 2)
	g.AddEdge(0, 2, 2)
	g.AddEdge(2, 3, 2)

	path := shortestPathOrFail(t, g, 0, 3)
	if len(path) != 1 || path[0] != direct {
		t.Fatalf("shortest path = %v, want [%d]", path, direct)
	}
	got, err := g.ReplacementPaths(0, 3, path)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].Reachable || got[0].Dist != 4 {
		t.Fatalf("got %+v, want dist 4 via an equal-length alternative", got[0])
	}
	checkReplacements(t, g, 0, 3, path, got)
}

func TestReplacementZeroWeightPathEdges(t *testing.T) {
	// The reference path itself contains zero-weight edges.
	g := New(4)
	g.AddEdge(0, 1, 0)
	g.AddEdge(1, 2, 0)
	g.AddEdge(2, 3, 1)
	g.AddEdge(0, 3, 9) // expensive direct arc

	path := shortestPathOrFail(t, g, 0, 3)
	if len(path) != 3 {
		t.Fatalf("shortest path = %v, want 3 edges", path)
	}
	got, err := g.ReplacementPaths(0, 3, path)
	if err != nil {
		t.Fatal(err)
	}
	// Removing any of the three path edges leaves only the direct arc.
	for _, r := range got {
		if !r.Reachable || r.Dist != 9 {
			t.Fatalf("remove edge %d: got %+v, want dist 9", r.RemovedEdge, r)
		}
	}
	checkReplacements(t, g, 0, 3, path, got)
}

func TestReplacementInvalidPath(t *testing.T) {
	g := New(3)
	e0 := g.AddEdge(0, 1, 1)
	e1 := g.AddEdge(1, 2, 1)
	if _, err := g.ReplacementPaths(0, 2, []int{e1}); err == nil {
		t.Fatal("broken chain accepted")
	}
	if _, err := g.ReplacementPaths(0, 2, []int{e0, e1, 99}); err == nil {
		t.Fatal("invalid edge ID accepted")
	}
	if _, err := g.ReplacementPaths(0, 2, []int{e0}); err == nil {
		t.Fatal("path ending at wrong node accepted")
	}
}

func TestReplacementRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(6) // 2..7 nodes
		g := New(n)
		m := rng.Intn(2 * n)
		for i := 0; i < m; i++ {
			// Parallel edges and zero weights are both likely.
			g.AddEdge(rng.Intn(n), rng.Intn(n), int64(rng.Intn(7)))
		}
		s, tgt := 0, n-1
		res := g.ShortestPath(s, tgt, NoEdge)
		if !res.Reachable {
			continue
		}
		got, err := g.ReplacementPaths(s, tgt, res.Path)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		checkReplacements(t, g, s, tgt, res.Path, got)
	}
}
