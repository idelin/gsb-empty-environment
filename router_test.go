package router

import (
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func mustGraph(t *testing.T, edges []Edge) *Graph {
	t.Helper()
	g, err := New(edges)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func mustForbid(t *testing.T, g *Graph, prev, next int) {
	t.Helper()
	if err := g.ForbidTurn(prev, next); err != nil {
		t.Fatalf("ForbidTurn(%d,%d): %v", prev, next, err)
	}
}

func mustWait(t *testing.T, g *Graph, prev, next int, w int64) {
	t.Helper()
	if err := g.SetTurnWait(prev, next, w); err != nil {
		t.Fatalf("SetTurnWait(%d,%d,%d): %v", prev, next, w, err)
	}
}

func checkRoute(t *testing.T, g *Graph, src, dst int, wantPath []int, wantCost int64) {
	t.Helper()
	path, cost, ok := g.ShortestPath(src, dst)
	if !ok {
		t.Fatalf("ShortestPath(%d,%d): no route, want %v cost %d", src, dst, wantPath, wantCost)
	}
	if cost != wantCost || !reflect.DeepEqual(path, wantPath) {
		t.Fatalf("ShortestPath(%d,%d) = %v cost %d, want %v cost %d", src, dst, path, cost, wantPath, wantCost)
	}
	// Cross-check against exhaustive enumeration.
	bPath, bCost, bOK := g.BruteForce(src, dst)
	if !bOK || bCost != wantCost || !reflect.DeepEqual(bPath, wantPath) {
		t.Fatalf("BruteForce(%d,%d) = %v cost %d ok=%v, want %v cost %d", src, dst, bPath, bCost, bOK, wantPath, wantCost)
	}
}

// Arriving at the same vertex via different edges must be treated as
// different states: the turn out of the vertex depends on the arrival edge.
// A vertex-based shortest-distance table would resolve A with cost 0 via
// edge 3 and then be stuck at the forbidden turn 3->4.
func TestArrivalEdgeState(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 0, To: 2, Time: 0},
		{ID: 3, From: 2, To: 1, Time: 0},
		{ID: 4, From: 1, To: 3, Time: 1},
	})
	mustForbid(t, g, 3, 4)
	checkRoute(t, g, 0, 3, []int{1, 4}, 2)
}

// A forbidden turn forces a detour even though the direct turn is shorter.
func TestForbiddenTurnDetour(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 1, To: 3, Time: 1}, // forbidden after 1
		{ID: 3, From: 1, To: 2, Time: 1},
		{ID: 4, From: 2, To: 3, Time: 5},
	})
	mustForbid(t, g, 1, 2)
	checkRoute(t, g, 0, 3, []int{1, 3, 4}, 7)
}

// Turn waits are charged between consecutive edges and can flip the choice
// between two vertex-disjoint routes.
func TestTurnWaitMatters(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 1, To: 3, Time: 1}, // cheap edges, expensive turn
		{ID: 3, From: 0, To: 2, Time: 2},
		{ID: 4, From: 2, To: 3, Time: 2}, // expensive edges, free turn
	})
	mustWait(t, g, 1, 2, 10)
	checkRoute(t, g, 0, 3, []int{3, 4}, 4)
}

// No turn wait is charged before the first edge of a route.
func TestNoWaitBeforeFirstEdge(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 3},
		{ID: 2, From: 1, To: 2, Time: 4},
	})
	mustWait(t, g, 1, 2, 5)
	checkRoute(t, g, 0, 2, []int{1, 2}, 12)
	checkRoute(t, g, 0, 1, []int{1}, 3)
}

// Equal-cost routes are ordered by the edge-id sequence, not by hop count
// or discovery order.
func TestLexicographicTieBreak(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 9, From: 0, To: 1, Time: 1},
		{ID: 5, From: 0, To: 2, Time: 1},
		{ID: 7, From: 1, To: 3, Time: 1},
		{ID: 8, From: 2, To: 3, Time: 1},
	})
	// Both routes cost 2: [9,7] vs [5,8]; [5,8] is lexicographically smaller.
	checkRoute(t, g, 0, 3, []int{5, 8}, 2)
}

// A tie broken after a common prefix.
func TestLexicographicTieBreakCommonPrefix(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 0},
		{ID: 4, From: 1, To: 2, Time: 1},
		{ID: 3, From: 1, To: 3, Time: 1},
		{ID: 9, From: 2, To: 4, Time: 1},
		{ID: 2, From: 3, To: 4, Time: 1},
	})
	// [1,4,9] vs [1,3,2], both cost 2; differ at position 1: 3 < 4.
	checkRoute(t, g, 0, 4, []int{1, 3, 2}, 2)
}

// Zero-cost cycles must neither loop forever nor be exploited to repeat a
// state for a lexicographically smaller sequence. Here [1,2,3,4] is a
// legitimate state-simple zero-cost route; repeating 2,3 (as in
// [1,2,3,2,3,4]) would be lexicographically smaller but repeats states on a
// zero-cost cycle and is forbidden.
func TestZeroCostCycleNoStateRepeat(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 0},
		{ID: 2, From: 1, To: 2, Time: 0},
		{ID: 3, From: 2, To: 1, Time: 0}, // closes a zero-cost cycle 2->3
		{ID: 4, From: 1, To: 3, Time: 0},
	})
	path, cost, ok := g.ShortestPath(0, 3)
	if !ok || cost != 0 {
		t.Fatalf("ShortestPath = %v cost %d ok=%v, want cost-0 route", path, cost, ok)
	}
	if !reflect.DeepEqual(path, []int{1, 2, 3, 4}) {
		t.Fatalf("ShortestPath = %v, want [1 2 3 4] (state repeats forbidden)", path)
	}
	seen := map[int]bool{}
	for _, id := range path {
		if seen[id] {
			t.Fatalf("route %v repeats state (edge) %d", path, id)
		}
		seen[id] = true
	}
}

// A zero-cost turn wait must not create an exploitable zero-cost cycle
// either.
func TestZeroCostTurnWaitCycle(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 1, To: 2, Time: 0},
		{ID: 3, From: 2, To: 1, Time: 0},
		{ID: 4, From: 2, To: 3, Time: 1},
	})
	mustWait(t, g, 2, 3, 0)
	mustWait(t, g, 3, 2, 0)
	checkRoute(t, g, 0, 3, []int{1, 2, 4}, 2)
}

func TestSourceEqualsDestination(t *testing.T) {
	g := mustGraph(t, []Edge{{ID: 1, From: 0, To: 1, Time: 1}})
	path, cost, ok := g.ShortestPath(0, 0)
	if !ok || cost != 0 || len(path) != 0 {
		t.Fatalf("ShortestPath(0,0) = %v cost %d ok=%v, want empty cost-0 route", path, cost, ok)
	}
}

func TestUnreachable(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 2, To: 3, Time: 1},
	})
	if _, _, ok := g.ShortestPath(0, 3); ok {
		t.Fatal("ShortestPath(0,3): got route, want unreachable")
	}
	if _, _, ok := g.BruteForce(0, 3); ok {
		t.Fatal("BruteForce(0,3): got route, want unreachable")
	}
}

// A route never passes through the destination and leaves again: truncation
// at the first arrival is always at least as good.
func TestRouteEndsAtFirstArrival(t *testing.T) {
	g := mustGraph(t, []Edge{
		{ID: 1, From: 0, To: 1, Time: 2},
		{ID: 2, From: 1, To: 2, Time: 0},
		{ID: 3, From: 2, To: 1, Time: 0},
	})
	checkRoute(t, g, 0, 1, []int{1}, 2)
}

func TestInvalidInput(t *testing.T) {
	if _, err := New([]Edge{{ID: 1, From: 0, To: 1, Time: 1}, {ID: 1, From: 0, To: 1, Time: 1}}); err == nil {
		t.Fatal("New: duplicate edge id accepted")
	}
	if _, err := New([]Edge{{ID: 1, From: 0, To: 1, Time: -1}}); err == nil {
		t.Fatal("New: negative travel time accepted")
	}
	g := mustGraph(t, []Edge{{ID: 1, From: 0, To: 1, Time: 1}})
	if err := g.SetTurnWait(1, 2, 0); err == nil {
		t.Fatal("SetTurnWait: unknown edge accepted")
	}
	if err := g.SetTurnWait(1, 1, -1); err == nil {
		t.Fatal("SetTurnWait: negative wait accepted")
	}
	if err := g.ForbidTurn(9, 1); err == nil {
		t.Fatal("ForbidTurn: unknown edge accepted")
	}
}

// validateRoute recomputes the cost of a route and checks every problem
// constraint: connectivity, turn restrictions, turn waits, no repeated
// state.
func validateRoute(t *testing.T, g *Graph, src, dst int, path []int, cost int64) {
	t.Helper()
	if len(path) == 0 {
		if src != dst {
			t.Fatalf("empty route but src=%d != dst=%d", src, dst)
		}
		if cost != 0 {
			t.Fatalf("empty route with cost %d", cost)
		}
		return
	}
	seen := map[int]bool{}
	var total int64
	prev := -1
	at := src
	for i, id := range path {
		idx, known := g.byID[id]
		if !known {
			t.Fatalf("route %v uses unknown edge %d", path, id)
		}
		if seen[id] {
			t.Fatalf("route %v repeats state (edge) %d", path, id)
		}
		seen[id] = true
		e := g.edges[idx]
		if e.From != at {
			t.Fatalf("route %v: edge %d starts at %d, want %d", path, id, e.From, at)
		}
		if i > 0 {
			wait, allowed := g.turnCost(prev, id)
			if !allowed {
				t.Fatalf("route %v uses forbidden turn %d->%d", path, prev, id)
			}
			total += wait
		}
		total += e.Time
		at = e.To
		prev = id
	}
	if at != dst {
		t.Fatalf("route %v ends at %d, want %d", path, at, dst)
	}
	if total != cost {
		t.Fatalf("route %v: reported cost %d, recomputed %d", path, cost, total)
	}
}

// TestFuzzAgainstBruteForce compares ShortestPath with exhaustive state
// expansion on many random small graphs, including zero-cost edges, zero
// turn waits, forbidden turns, parallel edges and self-loops.
func TestFuzzAgainstBruteForce(t *testing.T) {
	seeds := int64(5000)
	if v := os.Getenv("ROUTER_FUZZ_SEEDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("ROUTER_FUZZ_SEEDS: %v", err)
		}
		seeds = n
	}
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		numVertices := 2 + rng.Intn(5)
		numEdges := 1 + rng.Intn(9)
		edges := make([]Edge, numEdges)
		// Shuffle IDs so lexicographic order is independent of structure.
		ids := rng.Perm(numEdges)
		for i := range edges {
			edges[i] = Edge{
				ID:   ids[i] + 1,
				From: rng.Intn(numVertices),
				To:   rng.Intn(numVertices),
				Time: int64(rng.Intn(5)),
			}
		}
		g, err := New(edges)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		for _, e := range edges {
			for _, n := range edges {
				switch rng.Intn(6) {
				case 0:
					if err := g.ForbidTurn(e.ID, n.ID); err != nil {
						t.Fatalf("seed %d: %v", seed, err)
					}
				case 1:
					if err := g.SetTurnWait(e.ID, n.ID, int64(rng.Intn(4))); err != nil {
						t.Fatalf("seed %d: %v", seed, err)
					}
				}
			}
		}
		src := rng.Intn(numVertices)
		dst := rng.Intn(numVertices)

		path, cost, ok := g.ShortestPath(src, dst)
		bPath, bCost, bOK := g.BruteForce(src, dst)
		if ok != bOK || cost != bCost || !reflect.DeepEqual(path, bPath) {
			t.Fatalf("seed %d: ShortestPath(%d,%d) = %v cost %d ok=%v; BruteForce = %v cost %d ok=%v\nedges=%+v",
				seed, src, dst, path, cost, ok, bPath, bCost, bOK, edges)
		}
		if ok {
			validateRoute(t, g, src, dst, path, cost)
		}
	}
}
