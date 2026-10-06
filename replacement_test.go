package roadgraph

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates all simple paths (non-negative weights make simple
// paths sufficient even with zero-weight cycles) and returns the shortest
// distance from source to target with one edge excluded.
func bruteForce(g *Graph, source, target int, exclude EdgeID) (int64, bool) {
	var best int64 = Inf
	found := false
	visited := make([]bool, g.NumNodes)
	var dfs func(u int, acc int64)
	dfs = func(u int, acc int64) {
		if u == target {
			if acc < best {
				best = acc
			}
			found = true
			return
		}
		visited[u] = true
		for _, e := range g.adj[u] {
			if e.ID == exclude || visited[e.To] {
				continue
			}
			dfs(e.To, acc+e.Weight)
		}
		visited[u] = false
	}
	dfs(source, 0)
	return best, found
}

// checkWitness verifies that path is a real source-target chain in g that
// avoids removed and whose total weight equals dist.
func checkWitness(t *testing.T, g *Graph, source, target int, path []EdgeID, dist int64, removed EdgeID) {
	t.Helper()
	if len(path) == 0 {
		if source != target {
			t.Fatalf("empty witness for distinct endpoints %d -> %d", source, target)
		}
		return
	}
	cur := source
	var sum int64
	for _, id := range path {
		if id == removed {
			t.Fatalf("witness uses removed edge %d", id)
		}
		e, ok := g.Edge(id)
		if !ok {
			t.Fatalf("witness uses unknown edge %d", id)
		}
		if e.From != cur {
			t.Fatalf("witness broken at edge %d: starts %d, want %d", id, e.From, cur)
		}
		sum += e.Weight
		cur = e.To
	}
	if cur != target {
		t.Fatalf("witness ends at %d, want %d", cur, target)
	}
	if sum != dist {
		t.Fatalf("witness weight %d != reported distance %d", sum, dist)
	}
}

func mustAdd(t *testing.T, g *Graph, id EdgeID, from, to int, w int64) {
	t.Helper()
	if err := g.AddEdge(id, from, to, w); err != nil {
		t.Fatalf("AddEdge(%d): %v", id, err)
	}
}

func TestReplacementPathsParallelEdges(t *testing.T) {
	g := NewGraph(2)
	mustAdd(t, g, 1, 0, 1, 5)
	mustAdd(t, g, 2, 0, 1, 5) // parallel, equal cost
	mustAdd(t, g, 3, 0, 1, 7) // parallel, pricier

	reps, err := ReplacementPaths(g, 0, 1, []EdgeID{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 {
		t.Fatalf("got %d replacements, want 1", len(reps))
	}
	r := reps[0]
	if !r.Reachable || r.Distance != 5 {
		t.Fatalf("after removing edge 1: got reachable=%v dist=%d, want dist 5 via parallel edge 2", r.Reachable, r.Distance)
	}
	if len(r.Path) != 1 || r.Path[0] != 2 {
		t.Fatalf("witness = %v, want [2] (the sibling parallel edge, not the removed id)", r.Path)
	}
}

func TestReplacementPathsZeroWeightCycle(t *testing.T) {
	g := NewGraph(4)
	mustAdd(t, g, 1, 0, 1, 0)
	mustAdd(t, g, 2, 1, 2, 0)
	mustAdd(t, g, 3, 2, 1, 0) // zero-weight cycle 1 <-> 2
	mustAdd(t, g, 4, 2, 3, 5)
	mustAdd(t, g, 5, 0, 3, 7)
	mustAdd(t, g, 6, 1, 3, 4)

	dist, _, ok := g.ShortestPath(0, 3, nil)
	if !ok || dist != 4 {
		t.Fatalf("baseline shortest = %d, want 4", dist)
	}

	reps, err := ReplacementPaths(g, 0, 3, []EdgeID{1, 6})
	if err != nil {
		t.Fatal(err)
	}
	if reps[0].Distance != 7 || !reps[0].Reachable {
		t.Fatalf("remove edge 1: got %d, want 7 (direct edge 5)", reps[0].Distance)
	}
	checkWitness(t, g, 0, 3, reps[0].Path, reps[0].Distance, 1)
	if reps[1].Distance != 5 || !reps[1].Reachable {
		t.Fatalf("remove edge 6: got %d, want 5 (0->1->2->3 through the zero cycle)", reps[1].Distance)
	}
	checkWitness(t, g, 0, 3, reps[1].Path, reps[1].Distance, 6)
}

func TestReplacementPathsEqualShortestPaths(t *testing.T) {
	g := NewGraph(3)
	mustAdd(t, g, 1, 0, 2, 5) // direct, ties with the detour
	mustAdd(t, g, 2, 0, 1, 2)
	mustAdd(t, g, 3, 1, 2, 3)

	reps, err := ReplacementPaths(g, 0, 2, []EdgeID{1})
	if err != nil {
		t.Fatal(err)
	}
	r := reps[0]
	if !r.Reachable || r.Distance != 5 {
		t.Fatalf("got dist %d, want 5: an equal-length shortest path survives", r.Distance)
	}
	if len(r.Path) != 2 || r.Path[0] != 2 || r.Path[1] != 3 {
		t.Fatalf("witness = %v, want [2 3]", r.Path)
	}
}

func TestReplacementPathsUnreachable(t *testing.T) {
	g := NewGraph(3)
	mustAdd(t, g, 1, 0, 1, 1)
	mustAdd(t, g, 2, 1, 2, 1)

	reps, err := ReplacementPaths(g, 0, 2, []EdgeID{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reps {
		if r.Reachable {
			t.Fatalf("removing bridge edge %d must be unreachable", r.Removed)
		}
		if r.Distance != -1 {
			t.Fatalf("unreachable distance = %d, want explicit -1", r.Distance)
		}
		if r.Path != nil {
			t.Fatalf("unreachable witness = %v, want nil", r.Path)
		}
	}
}

func TestReplacementPathsInvalidPath(t *testing.T) {
	g := NewGraph(3)
	mustAdd(t, g, 1, 0, 1, 1)
	mustAdd(t, g, 2, 1, 2, 1)

	if _, err := ReplacementPaths(g, 0, 2, nil); err == nil {
		t.Fatal("empty path must be rejected")
	}
	if _, err := ReplacementPaths(g, 0, 2, []EdgeID{99}); err == nil {
		t.Fatal("unknown edge must be rejected")
	}
	if _, err := ReplacementPaths(g, 0, 2, []EdgeID{2}); err == nil {
		t.Fatal("broken chain must be rejected")
	}
	if _, err := ReplacementPaths(g, 0, 2, []EdgeID{1}); err == nil {
		t.Fatal("path ending short of target must be rejected")
	}
}

func TestReplacementPathsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(5) // 2..6 nodes
		g := NewGraph(n)
		m := 1 + rng.Intn(12)
		for i := 0; i < m; i++ {
			from := rng.Intn(n)
			to := rng.Intn(n)
			if to == from {
				to = (to + 1) % n
			}
			w := int64(rng.Intn(9)) // includes zero weights
			if err := g.AddEdge(EdgeID(1000*trial+i), from, to, w); err != nil {
				t.Fatal(err)
			}
		}
		source := rng.Intn(n)
		target := rng.Intn(n)
		if source == target {
			continue
		}
		_, path, ok := g.ShortestPath(source, target, nil)
		if !ok || len(path) == 0 {
			continue
		}
		reps, err := ReplacementPaths(g, source, target, path)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if len(reps) != len(path) {
			t.Fatalf("trial %d: got %d replacements for %d path edges", trial, len(reps), len(path))
		}
		for i, r := range reps {
			if r.Removed != path[i] {
				t.Fatalf("trial %d: replacement %d removes %d, want %d", trial, i, r.Removed, path[i])
			}
			best, found := bruteForce(g, source, target, r.Removed)
			if found != r.Reachable {
				t.Fatalf("trial %d edge %d: reachable=%v, brute force=%v", trial, r.Removed, r.Reachable, found)
			}
			if found && best != r.Distance {
				t.Fatalf("trial %d edge %d: dist=%d, brute force=%d", trial, r.Removed, r.Distance, best)
			}
			if found {
				checkWitness(t, g, source, target, r.Path, r.Distance, r.Removed)
			}
		}
	}
}
