package main

import (
	"math"
	"testing"
)

const eps = 1e-9

func edge(id string, from []string, to string, cost float64) Hyperedge {
	return Hyperedge{ID: id, Premises: from, Target: to, Cost: cost}
}

// Shared premise k feeds both a and b; the proof of c must charge k once.
func TestSharedPremiseChargedOnce(t *testing.T) {
	p := &Problem{
		Edges: []Hyperedge{
			edge("mk", nil, "k", 5),
			edge("ka", []string{"k"}, "a", 1),
			edge("kb", []string{"k"}, "b", 1),
			edge("abc", []string{"a", "b"}, "c", 1),
		},
	}
	res := p.Solve(0)
	nr := res.Nodes["c"]
	if !nr.Exact {
		t.Fatal("c should have an exact cost")
	}
	if nr.Cost != 8 {
		t.Fatalf("shared proof cost = %v, want 8 (k charged once)", nr.Cost)
	}
	if nr.Upper != 13 {
		t.Fatalf("tree-expansion upper bound = %v, want 13 (k counted twice)", nr.Upper)
	}
	if nr.Lower > nr.Cost+eps || nr.Upper < nr.Cost-eps {
		t.Fatalf("bounds [%v, %v] do not bracket %v", nr.Lower, nr.Upper, nr.Cost)
	}
	if len(nr.Proof) != 4 {
		t.Fatalf("proof should use 4 edges, got %d", len(nr.Proof))
	}
}

// A pure cycle with no base facts must not prove anything.
func TestCyclicSelfProofRejected(t *testing.T) {
	p := &Problem{
		Edges: []Hyperedge{
			edge("ab", []string{"a"}, "b", 1),
			edge("ba", []string{"b"}, "a", 1),
		},
	}
	res := p.Solve(0)
	if res.Nodes["a"].Derivable || res.Nodes["b"].Derivable {
		t.Fatal("cyclic self-proof must not be derivable")
	}
	aug := p.augmented()
	for _, target := range []string{"a", "b"} {
		cost, _, ok := enumMinCost(aug, target)
		if !ok {
			t.Fatal("enumeration should run")
		}
		if !math.IsInf(cost, 1) {
			t.Fatalf("enumeration derived %s with cost %v from a pure cycle", target, cost)
		}
	}
}

// A cycle anchored by a fact is fine: the fact breaks the circularity.
func TestCycleAnchoredByFact(t *testing.T) {
	p := &Problem{
		Facts: []Fact{{Name: "a", Cost: 0}},
		Edges: []Hyperedge{
			edge("ab", []string{"a"}, "b", 2),
			edge("ba", []string{"b"}, "a", 3),
			edge("bc", []string{"b"}, "c", 4),
			edge("cb", []string{"c"}, "b", 5),
		},
	}
	res := p.Solve(0)
	if got := res.Nodes["b"].Cost; got != 2 {
		t.Fatalf("cost(b) = %v, want 2", got)
	}
	if got := res.Nodes["c"].Cost; got != 6 {
		t.Fatalf("cost(c) = %v, want 6", got)
	}
}

// Several hyperedges may derive the same target; the cheapest proof wins.
func TestMultipleEdgesSameTarget(t *testing.T) {
	p := &Problem{
		Facts: []Fact{{Name: "x", Cost: 0}},
		Edges: []Hyperedge{
			edge("direct", []string{"x"}, "t", 5),
			edge("my", nil, "y", 1),
			edge("yt", []string{"y"}, "t", 1),
		},
	}
	res := p.Solve(0)
	nr := res.Nodes["t"]
	if nr.Cost != 2 {
		t.Fatalf("cost(t) = %v, want 2", nr.Cost)
	}
	if len(nr.Proof) != 2 {
		t.Fatalf("proof should use the 2-step chain, got %v", nr.Proof)
	}
}

// Facts with costs are charged only when actually used.
func TestFactCostChargedWhenUsed(t *testing.T) {
	p := &Problem{
		Facts: []Fact{{Name: "cheap", Cost: 1}, {Name: "pricey", Cost: 100}},
		Edges: []Hyperedge{
			edge("use-cheap", []string{"cheap"}, "t", 1),
		},
	}
	res := p.Solve(0)
	if got := res.Nodes["t"].Cost; got != 2 {
		t.Fatalf("cost(t) = %v, want 2 (pricey fact unused)", got)
	}
}

// The exact search must respect the state budget and still bracket the
// unfinished nodes with valid bounds.
func TestTruncation(t *testing.T) {
	p := &Problem{
		Edges: []Hyperedge{
			edge("e0", nil, "a", 1),
			edge("e1", []string{"a"}, "b", 1),
			edge("e2", []string{"b"}, "c", 1),
		},
	}
	res := p.Solve(1)
	if !res.Truncated {
		t.Fatal("expected truncation with maxStates=1")
	}
	nr := res.Nodes["c"]
	if nr.Exact {
		t.Fatal("c should not be exact after truncation")
	}
	if !(nr.Lower <= nr.Upper+eps) {
		t.Fatalf("inconsistent bounds [%v, %v]", nr.Lower, nr.Upper)
	}
}
