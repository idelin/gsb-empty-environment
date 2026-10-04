package main

import (
	"math/rand"
	"reflect"
	"testing"
)

// buildGraph constructs a Graph from costs and an edge list.
func buildGraph(costs []int64, edges [][2]int) *Graph {
	n := len(costs)
	g := &Graph{
		N:    n,
		Adj:  make([][]int, n),
		Radj: make([][]int, n),
		Cost: append([]int64(nil), costs...),
	}
	seen := make(map[[2]int]bool)
	for _, e := range edges {
		u, v := e[0], e[1]
		if seen[e] {
			continue
		}
		seen[e] = true
		g.Adj[u] = append(g.Adj[u], v)
		g.Radj[v] = append(g.Radj[v], u)
	}
	return g
}

// equalSet compares two sorted ID sets, treating nil and empty as equal.
func equalSet(a, b []int) bool {
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

func solveAndCheck(t *testing.T, g *Graph, wantCost int64, wantSet []int) {
	t.Helper()
	res := NewSolver(g, 0, 0).Solve()
	if !res.Optimal {
		t.Fatalf("search aborted without budget")
	}
	if res.Cost != wantCost || !reflect.DeepEqual(res.Set, wantSet) {
		t.Fatalf("got cost=%d set=%v, want cost=%d set=%v", res.Cost, res.Set, wantCost, wantSet)
	}
	if res.Lower != res.Upper || res.Upper != res.Cost {
		t.Fatalf("optimal result must have lower==upper==cost, got %d %d %d", res.Lower, res.Upper, res.Cost)
	}
	alive := make([]bool, g.N)
	for v := 0; v < g.N; v++ {
		alive[v] = true
	}
	for _, v := range res.Set {
		alive[v] = false
	}
	if !g.VerifyTopoOrder(alive, res.Topo) {
		t.Fatalf("invalid topo certificate %v for set %v", res.Topo, res.Set)
	}
}

func TestSelfLoopMandatory(t *testing.T) {
	// Single vertex with a self-loop must be deleted.
	g := buildGraph([]int64{5}, [][2]int{{0, 0}})
	solveAndCheck(t, g, 5, []int{0})
}

func TestSelfLoopAmongOthers(t *testing.T) {
	// Vertex 1 has a self-loop; cycle 0->2->0 also needs a hit.
	g := buildGraph([]int64{1, 7, 2}, [][2]int{{1, 1}, {0, 2}, {2, 0}})
	solveAndCheck(t, g, 8, []int{0, 1})
}

func TestZeroCostVertex(t *testing.T) {
	// Zero-cost vertex on the cycle is deleted for free.
	g := buildGraph([]int64{0, 3}, [][2]int{{0, 1}, {1, 0}})
	solveAndCheck(t, g, 0, []int{0})
}

func TestZeroCostTieBreakCount(t *testing.T) {
	// Two disjoint 2-cycles, all zero cost: must delete exactly 2 vertices,
	// lexicographically smallest set.
	g := buildGraph([]int64{0, 0, 0, 0}, [][2]int{{0, 1}, {1, 0}, {2, 3}, {3, 2}})
	solveAndCheck(t, g, 0, []int{0, 2})
}

func TestIntersectingCyclesSharedVertex(t *testing.T) {
	// Figure-eight: cycles {0,1} and {0,2} share vertex 0.
	// Deleting 0 costs 4; deleting 1 and 2 costs 2 -> prefer {1,2}.
	g := buildGraph([]int64{4, 1, 1}, [][2]int{{0, 1}, {1, 0}, {0, 2}, {2, 0}})
	solveAndCheck(t, g, 2, []int{1, 2})
	// Same shape, but shared vertex is cheap: delete {0}.
	g2 := buildGraph([]int64{1, 5, 5}, [][2]int{{0, 1}, {1, 0}, {0, 2}, {2, 0}})
	solveAndCheck(t, g2, 1, []int{0})
}

func TestSCCOneDeletionNotEnough(t *testing.T) {
	// K3 (complete digraph on 3 vertices) is one SCC but needs 2 deletions.
	edges := [][2]int{}
	for u := 0; u < 3; u++ {
		for v := 0; v < 3; v++ {
			if u != v {
				edges = append(edges, [2]int{u, v})
			}
		}
	}
	g := buildGraph([]int64{1, 1, 1}, edges)
	solveAndCheck(t, g, 2, []int{0, 1})
}

func TestRingOfCycles(t *testing.T) {
	// Three cycles chained in a ring; no single vertex hits all of them.
	// C1: 0-1-2, C2: 2-3-4, C3: 4-5-0 (edges i -> i+1 within each cycle).
	g := buildGraph([]int64{1, 1, 1, 1, 1, 1}, [][2]int{
		{0, 1}, {1, 2}, {2, 0},
		{2, 3}, {3, 4}, {4, 2},
		{4, 5}, {5, 0}, {0, 4},
	})
	res := NewSolver(g, 0, 0).Solve()
	if res.Cost != 2 || len(res.Set) != 2 {
		t.Fatalf("got cost=%d set=%v, want cost=2 count=2", res.Cost, res.Set)
	}
}

func TestBudgetGivesBounds(t *testing.T) {
	// A graph large enough that a tiny node budget interrupts the search.
	g := buildGraph([]int64{3, 1, 4, 1, 5, 9, 2, 6}, [][2]int{
		{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}, {4, 2},
		{4, 5}, {5, 6}, {6, 4}, {6, 7}, {7, 0}, {1, 5},
	})
	res := NewSolver(g, 1, 0).Solve()
	if res.Optimal {
		t.Fatalf("expected budget interruption")
	}
	bc, _, err := BruteForce(g)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lower > bc || res.Upper < bc {
		t.Fatalf("bounds [%d,%d] do not bracket optimum %d", res.Lower, res.Upper, bc)
	}
	if res.Upper != res.Cost {
		t.Fatalf("upper bound must equal incumbent cost")
	}
}

func TestRandomVsBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		n := 2 + rng.Intn(10) // 2..11 vertices
		costs := make([]int64, n)
		for i := range costs {
			costs[i] = int64(rng.Intn(6)) // includes zero-cost vertices
		}
		var edges [][2]int
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				p := 0.25
				if u == v {
					p = 0.10 // self-loops included
				}
				if rng.Float64() < p {
					edges = append(edges, [2]int{u, v})
				}
			}
		}
		g := buildGraph(costs, edges)
		res := NewSolver(g, 0, 0).Solve()
		if !res.Optimal {
			t.Fatalf("iter %d: aborted without budget", iter)
		}
		bc, bs, err := BruteForce(g)
		if err != nil {
			t.Fatal(err)
		}
		if res.Cost != bc || !equalSet(res.Set, bs) {
			t.Fatalf("iter %d: solver cost=%d set=%v, brute cost=%d set=%v\ncosts=%v edges=%v",
				iter, res.Cost, res.Set, bc, bs, costs, edges)
		}
		alive := make([]bool, n)
		for v := 0; v < n; v++ {
			alive[v] = true
		}
		for _, v := range res.Set {
			alive[v] = false
		}
		if !g.VerifyTopoOrder(alive, res.Topo) {
			t.Fatalf("iter %d: invalid certificate", iter)
		}
	}
}

func TestRandomBudgetBoundsVsBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 100; iter++ {
		n := 6 + rng.Intn(6)
		costs := make([]int64, n)
		for i := range costs {
			costs[i] = int64(rng.Intn(6))
		}
		var edges [][2]int
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				if rng.Float64() < 0.3 {
					edges = append(edges, [2]int{u, v})
				}
			}
		}
		g := buildGraph(costs, edges)
		bc, _, err := BruteForce(g)
		if err != nil {
			t.Fatal(err)
		}
		budget := int64(1 + rng.Intn(20))
		res := NewSolver(g, budget, 0).Solve()
		if res.Lower > bc || res.Upper < bc {
			t.Fatalf("iter %d: bounds [%d,%d] miss optimum %d", iter, res.Lower, res.Upper, bc)
		}
		if res.Optimal && res.Cost != bc {
			t.Fatalf("iter %d: completed search gave %d, optimum is %d", iter, res.Cost, bc)
		}
	}
}
