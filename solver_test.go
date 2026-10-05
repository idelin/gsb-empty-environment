package treemedian

import (
	"math/rand"
	"testing"
)

// bruteCost enumerates every facility subset of size <= K on allowed nodes
// and returns the optimal weighted distance.
func bruteCost(s *Solver) (int64, bool) {
	n := s.N
	var allowed []int
	for i := 0; i < n; i++ {
		if !s.Forbidden[i] {
			allowed = append(allowed, i)
		}
	}
	best := Inf
	m := len(allowed)
	for mask := 0; mask < (1 << uint(m)); mask++ {
		if popcount(mask) > s.K {
			continue
		}
		if mask == 0 {
			// Empty placement: feasible only when no demand exists.
			ok := true
			for v := 0; v < n; v++ {
				if s.Demand[v] != 0 {
					ok = false
					break
				}
			}
			if ok {
				best = 0
			}
			continue
		}
		var cost int64
		for v := 0; v < n; v++ {
			d := Inf
			for i, f := range allowed {
				if mask&(1<<uint(i)) != 0 && s.dist[v][f] < d {
					d = s.dist[v][f]
				}
			}
			cost += s.Demand[v] * d
		}
		if cost < best {
			best = cost
		}
	}
	return best, best != Inf
}

func popcount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

// checkResult validates feasibility, optimality against brute force, and the
// assignment tie-break rule (nearest facility, ties to smaller id).
func checkResult(t *testing.T, s *Solver, res Result) {
	t.Helper()
	best, feasible := bruteCost(s)
	if res.Feasible != feasible {
		t.Fatalf("feasible=%v, brute says %v", res.Feasible, feasible)
	}
	if !feasible {
		return
	}
	if res.Cost != best {
		t.Fatalf("cost=%d, brute optimum=%d", res.Cost, best)
	}
	if len(res.Facilities) > s.K {
		t.Fatalf("too many facilities: %v (K=%d)", res.Facilities, s.K)
	}
	seen := make(map[int]bool)
	for _, f := range res.Facilities {
		if s.Forbidden[f] {
			t.Fatalf("facility %d on forbidden node", f)
		}
		if seen[f] {
			t.Fatalf("duplicate facility %d", f)
		}
		seen[f] = true
	}
	// Recompute cost and assignment independently.
	var cost int64
	for v := 0; v < s.N; v++ {
		if len(res.Facilities) == 0 {
			if res.Assignment[v] != -1 {
				t.Fatalf("node %d assigned with no facilities", v)
			}
			continue
		}
		bestD, bestF := Inf, -1
		for _, f := range res.Facilities {
			d := s.dist[v][f]
			if d < bestD || (d == bestD && f < bestF) {
				bestD, bestF = d, f
			}
		}
		if res.Assignment[v] != bestF {
			t.Fatalf("node %d assigned to %d, want %d", v, res.Assignment[v], bestF)
		}
		cost += s.Demand[v] * bestD
	}
	if cost != res.Cost {
		t.Fatalf("reported cost %d, recomputed %d", res.Cost, cost)
	}
}

func TestAgainstBruteForceRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(10) // 1..10 nodes
		K := rng.Intn(4)      // 0..3
		if rng.Intn(5) == 0 {
			K = n + rng.Intn(2) // sometimes K >= n
		}
		s := NewSolver(n, K)
		for v := 1; v < n; v++ {
			p := rng.Intn(v)
			w := int64(rng.Intn(4)) // 0..3, includes zero-length edges
			s.AddEdge(p, v, w)
		}
		for v := 0; v < n; v++ {
			s.Demand[v] = int64(rng.Intn(5)) // 0..4
			s.Forbidden[v] = rng.Intn(3) == 0
		}
		res := s.Solve()
		if !res.Exact {
			t.Fatalf("iter %d: expected exact result on small instance", iter)
		}
		checkResult(t, s, res)
	}
}

func TestZeroLengthEdges(t *testing.T) {
	// All edges zero: any single facility serves everything at cost 0.
	s := NewSolver(4, 1)
	s.AddEdge(0, 1, 0)
	s.AddEdge(1, 2, 0)
	s.AddEdge(1, 3, 0)
	for v := 0; v < 4; v++ {
		s.Demand[v] = 7
	}
	res := s.Solve()
	if !res.Feasible || res.Cost != 0 {
		t.Fatalf("zero-edge tree: got feasible=%v cost=%d", res.Feasible, res.Cost)
	}
	if len(res.Facilities) != 1 {
		t.Fatalf("expected 1 facility, got %v", res.Facilities)
	}
	for v := 0; v < 4; v++ {
		if res.Assignment[v] != res.Facilities[0] {
			t.Fatalf("node %d should attach to the only facility", v)
		}
	}
}

func TestAllForbidden(t *testing.T) {
	// Positive demand, nothing allowed: infeasible.
	s := NewSolver(3, 2)
	s.AddEdge(0, 1, 1)
	s.AddEdge(1, 2, 1)
	for v := 0; v < 3; v++ {
		s.Demand[v] = 1
		s.Forbidden[v] = true
	}
	res := s.Solve()
	if res.Feasible {
		t.Fatalf("all forbidden with demand must be infeasible")
	}
	// Zero demand: feasible with empty placement and cost 0.
	s2 := NewSolver(3, 2)
	s2.AddEdge(0, 1, 1)
	s2.AddEdge(1, 2, 1)
	for v := 0; v < 3; v++ {
		s2.Forbidden[v] = true
	}
	res2 := s2.Solve()
	if !res2.Feasible || res2.Cost != 0 || len(res2.Facilities) != 0 {
		t.Fatalf("all forbidden zero demand: %+v", res2)
	}
	for v := 0; v < 3; v++ {
		if res2.Assignment[v] != -1 {
			t.Fatalf("node %d should be unassigned", v)
		}
	}
}

func TestKZero(t *testing.T) {
	s := NewSolver(2, 0)
	s.AddEdge(0, 1, 5)
	s.Demand[0] = 3
	res := s.Solve()
	if res.Feasible {
		t.Fatalf("K=0 with positive demand must be infeasible")
	}
}

func TestSingleNode(t *testing.T) {
	s := NewSolver(1, 1)
	s.Demand[0] = 9
	res := s.Solve()
	if !res.Feasible || res.Cost != 0 || len(res.Facilities) != 1 || res.Facilities[0] != 0 {
		t.Fatalf("single node: %+v", res)
	}
	if res.Assignment[0] != 0 {
		t.Fatalf("single node assignment: %v", res.Assignment)
	}
}

func TestTieBreakSmallerFacilityID(t *testing.T) {
	// Path 0-1-2, unit edges, heavy demand at the ends: the unique optimum
	// places facilities at 0 and 2; node 1 is equidistant and must attach
	// to the smaller facility id (0).
	s := NewSolver(3, 2)
	s.AddEdge(0, 1, 1)
	s.AddEdge(1, 2, 1)
	s.Demand[0], s.Demand[1], s.Demand[2] = 10, 1, 10
	res := s.Solve()
	if res.Cost != 1 || len(res.Facilities) != 2 || res.Facilities[0] != 0 || res.Facilities[1] != 2 {
		t.Fatalf("unexpected placement: %+v", res)
	}
	if res.Assignment[1] != 0 {
		t.Fatalf("equidistant node must attach to smaller facility id, got %d", res.Assignment[1])
	}
}

func TestBudgetExhaustedGivesUpperBound(t *testing.T) {
	n, K := 25, 3
	build := func() *Solver {
		rng := rand.New(rand.NewSource(7))
		s := NewSolver(n, K)
		for v := 1; v < n; v++ {
			s.AddEdge(rng.Intn(v), v, int64(1+rng.Intn(5)))
		}
		for v := 0; v < n; v++ {
			s.Demand[v] = int64(rng.Intn(6))
		}
		return s
	}
	s1 := build()
	exact := s1.Solve()
	if !exact.Exact || !exact.Feasible {
		t.Fatalf("baseline should be exact and feasible")
	}
	s2 := build()
	s2.MaxOps = 1 // force immediate exhaustion
	bounded := s2.Solve()
	if bounded.Exact {
		t.Fatalf("tiny budget must yield an inexact (upper bound) result")
	}
	if !bounded.Feasible {
		t.Fatalf("greedy fallback must stay feasible")
	}
	if bounded.Cost < exact.Cost {
		t.Fatalf("upper bound %d below optimum %d", bounded.Cost, exact.Cost)
	}
	if len(bounded.Facilities) > K {
		t.Fatalf("greedy placed too many facilities")
	}
}

func TestAtMostKWithZeroDemand(t *testing.T) {
	// Demand concentrated at node 0: one facility suffices, must not place more.
	s := NewSolver(5, 4)
	s.AddEdge(0, 1, 2)
	s.AddEdge(1, 2, 2)
	s.AddEdge(1, 3, 2)
	s.AddEdge(3, 4, 2)
	s.Demand[0] = 5
	res := s.Solve()
	if !res.Feasible || res.Cost != 0 {
		t.Fatalf("got %+v", res)
	}
	if len(res.Facilities) != 1 || res.Facilities[0] != 0 {
		t.Fatalf("expected single facility at 0, got %v", res.Facilities)
	}
}
