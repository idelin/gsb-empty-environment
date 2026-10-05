package hgsolve

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func TestCyclicSelfProofRejected(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: "ab", From: []string{"a"}, To: "b", Cost: 1},
		{ID: "ba", From: []string{"b"}, To: "a", Cost: 1},
	})
	if got := g.Derivable(nil); len(got) != 0 {
		t.Fatalf("cycle with no grounding derived %v", got)
	}
	if _, ok := g.SolveExact(nil, "a"); ok {
		t.Fatal("SolveExact proved a from a pure cycle")
	}
	if _, _, ok := g.BruteForce(nil, "a"); ok {
		t.Fatal("BruteForce proved a from a pure cycle")
	}
	// Grounding the cycle at a makes both derivable.
	if got := g.Derivable([]string{"a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Derivable = %v", got)
	}
	res, ok := g.SolveExact([]string{"a"}, "b")
	if !ok || res.Cost != 1 {
		t.Fatalf("SolveExact = %+v, ok=%v", res, ok)
	}
}

func TestSharedPremiseChargedOnce(t *testing.T) {
	// c is shared by the proofs of d and e; a tree charges it twice.
	g := NewGraph([]Edge{
		{ID: "mk-c", From: []string{"a", "b"}, To: "c", Cost: 5},
		{ID: "mk-d", From: []string{"c"}, To: "d", Cost: 1},
		{ID: "mk-e", From: []string{"c"}, To: "e", Cost: 1},
		{ID: "mk-f", From: []string{"d", "e"}, To: "f", Cost: 1},
	})
	facts := []string{"a", "b"}

	tree, ok := g.TreeCost(facts, "f")
	if !ok || tree != 13 {
		t.Fatalf("TreeCost = %v, ok=%v, want 13", tree, ok)
	}
	res, ok := g.SolveExact(facts, "f")
	if !ok {
		t.Fatal("SolveExact: f not derivable")
	}
	if res.Cost != 8 {
		t.Fatalf("exact DAG cost = %v, want 8 (tree overestimates as 13)", res.Cost)
	}
	want := []string{"mk-c", "mk-d", "mk-e", "mk-f"}
	if !reflect.DeepEqual(res.EdgeIDs, want) {
		t.Fatalf("proof = %v, want %v", res.EdgeIDs, want)
	}
	if err := g.VerifyProof(facts, "f", res.EdgeIDs, res.Cost); err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
	bc, _, ok := g.BruteForce(facts, "f")
	if !ok || bc != 8 {
		t.Fatalf("BruteForce = %v, ok=%v, want 8", bc, ok)
	}
	lb, ok := g.OrLower(facts, "f")
	if !ok || lb > 8 {
		t.Fatalf("lower bound %v exceeds optimum 8", lb)
	}
}

func TestMultipleProducersSameTarget(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: "via-a", From: []string{"a"}, To: "c", Cost: 5},
		{ID: "via-b", From: []string{"b"}, To: "c", Cost: 2},
		{ID: "via-ab", From: []string{"a", "b"}, To: "c", Cost: 3},
	})
	res, ok := g.SolveExact([]string{"a", "b"}, "c")
	if !ok || res.Cost != 2 || !reflect.DeepEqual(res.EdgeIDs, []string{"via-b"}) {
		t.Fatalf("SolveExact = %+v, ok=%v", res, ok)
	}
}

func TestZeroPremiseAndZeroCost(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: "axiom", From: nil, To: "x", Cost: 0},
		{ID: "step", From: []string{"x"}, To: "y", Cost: 4},
	})
	if got := g.Derivable(nil); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("Derivable = %v", got)
	}
	res, ok := g.SolveExact(nil, "y")
	if !ok || res.Cost != 4 {
		t.Fatalf("SolveExact = %+v, ok=%v", res, ok)
	}
}

func TestFactTargetIsFree(t *testing.T) {
	g := NewGraph([]Edge{{ID: "e", From: []string{"a"}, To: "b", Cost: 3}})
	res, ok := g.SolveExact([]string{"a"}, "a")
	if !ok || res.Cost != 0 || len(res.EdgeIDs) != 0 {
		t.Fatalf("SolveExact = %+v, ok=%v", res, ok)
	}
}

func TestUnreachableTarget(t *testing.T) {
	g := NewGraph([]Edge{{ID: "e", From: []string{"a"}, To: "b", Cost: 1}})
	if _, ok := g.SolveExact(nil, "b"); ok {
		t.Fatal("b should be unreachable without facts")
	}
	if _, ok := g.SolveExact(nil, "ghost"); ok {
		t.Fatal("unknown node should be unreachable")
	}
}

// TestCrossCheckRandom compares SolveExact against exhaustive subset
// enumeration on many small random hypergraphs, and checks that the cheap
// bounds bracket the optimum.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		nNodes := 2 + rng.Intn(5)
		names := make([]string, nNodes)
		for i := range names {
			names[i] = fmt.Sprintf("n%d", i)
		}
		nEdges := 1 + rng.Intn(10)
		edges := make([]Edge, nEdges)
		for i := range edges {
			k := rng.Intn(4) // 0..3 premises, self-loops allowed
			prem := make([]string, 0, k)
			for j := 0; j < k; j++ {
				prem = append(prem, names[rng.Intn(nNodes)])
			}
			edges[i] = Edge{
				ID:   fmt.Sprintf("e%d", i),
				From: prem,
				To:   names[rng.Intn(nNodes)],
				Cost: float64(rng.Intn(10)),
			}
		}
		var facts []string
		for _, n := range names {
			if rng.Intn(3) == 0 {
				facts = append(facts, n)
			}
		}
		g := NewGraph(edges)
		for _, target := range names {
			bc, bids, bok := g.BruteForce(facts, target)
			res, eok := g.SolveExact(facts, target)
			if bok != eok {
				t.Fatalf("trial %d target %s: reachability mismatch brute=%v exact=%v\nedges=%v facts=%v",
					trial, target, bok, eok, edges, facts)
			}
			if !bok {
				continue
			}
			if math.Abs(bc-res.Cost) > 1e-9 {
				t.Fatalf("trial %d target %s: brute=%v exact=%v\nedges=%v facts=%v",
					trial, target, bc, res.Cost, edges, facts)
			}
			if err := g.VerifyProof(facts, target, res.EdgeIDs, res.Cost); err != nil {
				t.Fatalf("trial %d target %s: invalid exact proof: %v\nedges=%v facts=%v",
					trial, target, err, edges, facts)
			}
			if err := g.VerifyProof(facts, target, bids, bc); err != nil {
				t.Fatalf("trial %d target %s: invalid brute proof: %v", trial, target, err)
			}
			if lb, ok := g.OrLower(facts, target); ok && lb > bc+1e-9 {
				t.Fatalf("trial %d target %s: lower bound %v > optimum %v", trial, target, lb, bc)
			}
			if ub, ok := g.TreeCost(facts, target); ok && ub < bc-1e-9 {
				t.Fatalf("trial %d target %s: tree cost %v < optimum %v (sharing must only help)",
					trial, target, ub, bc)
			}
			if gc, _, ok := g.GreedyProof(facts, target); !ok || gc < bc-1e-9 {
				t.Fatalf("trial %d target %s: greedy %v (ok=%v) < optimum %v", trial, target, gc, ok, bc)
			}
		}
	}
}

// TestSharingBeatsGreedy constructs a case where the greedy upper bound is
// strictly suboptimal, forcing the exact search to improve on it: deriving
// both c and d from the shared node b costs 5+1+1+1=8, but greedy first
// derives c cheaply via a and ends up paying 1+2+5+1+1=10.
func TestSharingBeatsGreedy(t *testing.T) {
	g := NewGraph([]Edge{
		{ID: "s-a", From: []string{"s"}, To: "a", Cost: 1},
		{ID: "a-c", From: []string{"a"}, To: "c", Cost: 2},
		{ID: "s-b", From: []string{"s"}, To: "b", Cost: 5},
		{ID: "a-d", From: []string{"a"}, To: "d", Cost: 20},
		{ID: "b-c", From: []string{"b"}, To: "c", Cost: 1},
		{ID: "b-d", From: []string{"b"}, To: "d", Cost: 1},
		{ID: "cd-t", From: []string{"c", "d"}, To: "t", Cost: 1},
	})
	gc, _, ok := g.GreedyProof([]string{"s"}, "t")
	if !ok || gc != 10 {
		t.Fatalf("greedy = %v, ok=%v, want 10", gc, ok)
	}
	res, ok := g.SolveExact([]string{"s"}, "t")
	if !ok || res.Cost != 8 {
		t.Fatalf("exact = %+v, ok=%v, want cost 8", res, ok)
	}
	if !reflect.DeepEqual(res.EdgeIDs, []string{"b-c", "b-d", "cd-t", "s-b"}) {
		t.Fatalf("proof = %v", res.EdgeIDs)
	}
}
