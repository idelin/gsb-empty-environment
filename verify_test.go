package wafdecision

import (
	"math"
	"testing"
)

func TestVerifyAllAssignments(t *testing.T) {
	formula := OrOf(
		AndOf(NewLeaf(0), NotOf(NewLeaf(1))),
		AndOf(NewLeaf(2), NewLeaf(1)),
	)
	specs := []LeafSpec{
		spec(0, 3, 0.1, 0.25),
		spec(1, 7, 0.4, 0.6),
		spec(2, 2, 0.8, 0.95),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rep, err := g.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Assignments != 8 {
		t.Fatalf("assignments = %d, want 8", rep.Assignments)
	}
	if rep.MaxObservedCost != g.WorstCost {
		t.Fatalf("bound mismatch: %d vs %d", rep.MaxObservedCost, g.WorstCost)
	}
	if rep.MaxCalls > len(specs) {
		t.Fatalf("max calls = %d", rep.MaxCalls)
	}
	if rep.ExpectedCostLo > rep.ExpectedCostHi {
		t.Fatal("expected cost bounds inverted")
	}
	if rep.ExpectedCostMid < rep.ExpectedCostLo-1e-9 ||
		rep.ExpectedCostMid > rep.ExpectedCostHi+1e-9 {
		t.Fatal("midpoint expected cost outside box bounds")
	}
	if math.Abs(rep.ExpectedCostMid-g.MidExpectedCost) > 1e-9 {
		t.Fatalf("midpoint expected cost mismatch: %v vs %v",
			rep.ExpectedCostMid, g.MidExpectedCost)
	}
}

func TestPointIntervalsCollapseBox(t *testing.T) {
	formula := AndOf(NewLeaf(0), NewLeaf(1))
	specs := []LeafSpec{
		spec(0, 4, 0.5, 0.5),
		spec(1, 6, 0.25, 0.25),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rep, err := g.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if math.Abs(rep.ExpectedCostLo-rep.ExpectedCostHi) > 1e-12 {
		t.Fatalf("point intervals must collapse box: lo=%v hi=%v",
			rep.ExpectedCostLo, rep.ExpectedCostHi)
	}
	// Leaf 0 always runs (cost 4); leaf 1 (cost 6) runs only when leaf 0 is
	// true (p=0.5), regardless of leaf 1's own selectivity.
	want := 4.0 + 6.0*0.5
	if math.Abs(rep.ExpectedCostLo-want) > 1e-9 {
		t.Fatalf("expected cost = %v, want %v", rep.ExpectedCostLo, want)
	}
}

func TestVerifyDetectsTamperedGraph(t *testing.T) {
	formula := AndOf(NewLeaf(0), NewLeaf(1))
	specs := []LeafSpec{spec(0, 1, 0.5, 0.5), spec(1, 1, 0.5, 0.5)}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Corrupt the root's true successor to flip a verdict; verification must
	// detect the semantic mismatch across the exhaustive assignments.
	root := g.Root()
	original := g.nodes[root].OnTrue
	g.nodes[root].OnTrue = g.nodes[root].OnFalse
	if _, err := g.Verify(); err == nil {
		t.Fatal("tampered graph passed verification")
	}
	g.nodes[root].OnTrue = original
	if _, err := g.Verify(); err != nil {
		t.Fatalf("restored graph should verify: %v", err)
	}
}
