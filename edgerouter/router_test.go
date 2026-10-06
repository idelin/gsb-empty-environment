package edgerouter

import (
	"math/rand"
	"reflect"
	"testing"
)

func checkPath(t *testing.T, g *Graph, src, dst int, wantTotal int64, wantEdges []int) {
	t.Helper()
	got, ok := g.ShortestPath(src, dst)
	if !ok {
		t.Fatalf("ShortestPath(%d,%d): no path, want %v", src, dst, wantEdges)
	}
	if got.Total != wantTotal || !reflect.DeepEqual(got.Edges, wantEdges) {
		t.Fatalf("ShortestPath(%d,%d) = %v, want total=%d edges=%v", src, dst, got, wantTotal, wantEdges)
	}
	ref, ok := g.Enumerate(src, dst)
	if !ok {
		t.Fatalf("Enumerate(%d,%d): no path, want %v", src, dst, wantEdges)
	}
	if ref.Total != wantTotal || !reflect.DeepEqual(ref.Edges, wantEdges) {
		t.Fatalf("Enumerate(%d,%d) = %v, want total=%d edges=%v", src, dst, ref, wantTotal, wantEdges)
	}
}

// Two parallel edges enter vertex u; the cheaper one may not turn onto the
// only exit. Recording one shortest distance per vertex loses the route.
func TestForbiddenTurnBeatsVertexState(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 1}, // cheap entry, turn forbidden
		{ID: 2, From: 0, To: 1, Time: 2}, // expensive entry, turn allowed
		{ID: 3, From: 1, To: 2, Time: 1},
	})
	g.ForbidTurn(1, 3)
	checkPath(t, g, 0, 2, 3, []int{2, 3})
}

// Turn waiting time makes the more expensive entry edge cheaper overall.
func TestTurnWaitingTime(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 0, To: 1, Time: 2},
		{ID: 3, From: 1, To: 2, Time: 1},
	})
	// Without waiting times [1 3] costs 2 and wins; a wait of 5 on the
	// turn from edge 1 makes the route via edge 2 cheaper overall.
	g.SetTurnWait(1, 3, 5)
	checkPath(t, g, 0, 2, 3, []int{2, 3})
}

// Equal total time: the lexicographically smaller edge-ID sequence wins,
// including when sequences first differ past the initial edge.
func TestLexicographicTieBreak(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: 5, From: 0, To: 1, Time: 1},
		{ID: 2, From: 0, To: 1, Time: 1},
		{ID: 3, From: 1, To: 2, Time: 1},
	})
	checkPath(t, g, 0, 2, 2, []int{2, 3})

	// Same cost, shared prefix, divergence at the second edge.
	h := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 4, From: 1, To: 2, Time: 1},
		{ID: 2, From: 1, To: 3, Time: 1},
		{ID: 3, From: 3, To: 2, Time: 1},
	})
	// [1 4] costs 2, [1 2 3] costs 3: cost dominates lexicography.
	checkPath(t, h, 0, 2, 2, []int{1, 4})

	// Force equal cost: now [1 2 3] must beat [1 4] because 2 < 4.
	k := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 4, From: 1, To: 2, Time: 2},
		{ID: 2, From: 1, To: 3, Time: 1},
		{ID: 3, From: 3, To: 2, Time: 1},
	})
	checkPath(t, k, 0, 2, 3, []int{1, 2, 3})
}

// Zero-cost edges forming a cycle: the search must terminate, never repeat
// a state, and still return the minimum-time route.
func TestZeroCostCycle(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 0},
		{ID: 2, From: 1, To: 2, Time: 0},
		{ID: 3, From: 2, To: 1, Time: 0}, // zero-cost cycle 1<->2
		{ID: 4, From: 2, To: 3, Time: 1},
		{ID: 5, From: 1, To: 3, Time: 5},
	})
	got, ok := g.ShortestPath(0, 3)
	if !ok {
		t.Fatal("ShortestPath: no path")
	}
	if got.Total != 1 || !reflect.DeepEqual(got.Edges, []int{1, 2, 4}) {
		t.Fatalf("ShortestPath = %v, want total=1 edges=[1 2 4]", got)
	}
	seen := make(map[int]bool)
	for _, id := range got.Edges {
		if seen[id] {
			t.Fatalf("state %d repeated in %v", id, got.Edges)
		}
		seen[id] = true
	}
	ref, _ := g.Enumerate(0, 3)
	if !reflect.DeepEqual(got, ref) {
		t.Fatalf("mismatch: ShortestPath=%v Enumerate=%v", got, ref)
	}
}

// All continuations forbidden: report no path.
func TestUnreachable(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: 1, From: 0, To: 1, Time: 1},
		{ID: 2, From: 1, To: 2, Time: 1},
	})
	g.ForbidTurn(1, 2)
	if _, ok := g.ShortestPath(0, 2); ok {
		t.Fatal("ShortestPath: unexpected path")
	}
	if _, ok := g.Enumerate(0, 2); ok {
		t.Fatal("Enumerate: unexpected path")
	}
}

// Source equals destination: empty path of zero cost.
func TestSourceEqualsDestination(t *testing.T) {
	g := NewGraph([]Edge{{ID: 1, From: 0, To: 1, Time: 1}})
	got, ok := g.ShortestPath(2, 2)
	if !ok || got.Total != 0 || len(got.Edges) != 0 {
		t.Fatalf("ShortestPath(2,2) = %v, %v", got, ok)
	}
}

// Cross-check ShortestPath against exhaustive enumeration on many random
// small graphs with zero-cost edges, turn waits, and forbidden turns.
func TestRandomAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 2000; trial++ {
		n := 2 + rng.Intn(4) // 2..5 vertices
		var edges []Edge
		id := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j || rng.Intn(2) == 0 {
					continue
				}
				edges = append(edges, Edge{ID: id, From: i, To: j, Time: int64(rng.Intn(5))})
				id++
				if rng.Intn(4) == 0 { // occasional parallel edge
					edges = append(edges, Edge{ID: id, From: i, To: j, Time: int64(rng.Intn(5))})
					id++
				}
			}
		}
		if len(edges) == 0 {
			continue
		}
		// Shuffle IDs so lexicographic order differs from construction order.
		perm := rng.Perm(len(edges))
		for i := range edges {
			edges[i].ID = perm[i]
		}
		g := NewGraph(edges)
		for _, a := range edges {
			for _, b := range edges {
				if a.To != b.From {
					continue
				}
				switch rng.Intn(4) {
				case 0:
					g.ForbidTurn(a.ID, b.ID)
				case 1:
					g.SetTurnWait(a.ID, b.ID, int64(rng.Intn(4)))
				}
			}
		}
		src, dst := rng.Intn(n), rng.Intn(n)
		got, gotOK := g.ShortestPath(src, dst)
		want, wantOK := g.Enumerate(src, dst)
		if gotOK != wantOK {
			t.Fatalf("trial %d: existence mismatch dijkstra=%v enum=%v\nedges=%+v src=%d dst=%d",
				trial, gotOK, wantOK, edges, src, dst)
		}
		if !wantOK {
			continue
		}
		if got.Total != want.Total || !reflect.DeepEqual(got.Edges, want.Edges) {
			t.Fatalf("trial %d: dijkstra=%v enum=%v\nedges=%+v src=%d dst=%d",
				trial, got, want, edges, src, dst)
		}
	}
}
