package netsec

import "testing"

func TestDeeplyNestedLoopsMerge(t *testing.T) {
	// 2 outer x 3 inner iterations with a flip inside; enumeration yields
	// 2^(2*3) = 64 paths that the merging engine aggregates into one outcome.
	inner := Repeat{Count: 3, Body: []Stmt{
		Flip{NodeID: "bit", Branches: []Branch{
			{Weight: rat("1/2"), Body: []Stmt{Observe{Label: "b", Expr: Lit(true)}}},
			{Weight: rat("1/2"), Body: []Stmt{Observe{Label: "b", Expr: Lit(false)}}},
		}},
	}}
	p := &Program{Body: []Stmt{Repeat{Count: 2, Body: []Stmt{inner}}}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Paths != 64 {
		t.Fatalf("paths = %d, want 64", rep.Paths)
	}
	total := rat("0")
	for _, pt := range rep.Distribution.Points {
		total.Add(total, pt.Prob)
	}
	if total.Cmp(rat("1")) != 0 {
		t.Fatalf("total mass = %s, want 1", total)
	}
}

func TestFailBetweenObservations(t *testing.T) {
	// The exception outcome keeps only observations emitted before the Fail,
	// even when nested inside a flip branch and a loop.
	p := &Program{Body: []Stmt{
		Observe{Label: "before", Expr: Lit(true)},
		Repeat{Count: 3, Body: []Stmt{
			Flip{NodeID: "f", Branches: []Branch{
				{Weight: rat("1/4"), Body: []Stmt{Fail{Name: "early"}}},
				{Weight: rat("3/4"), Body: []Stmt{Observe{Label: "tick", Expr: Lit(true)}}},
			}},
		}},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	failure := rep.Distribution.FailureProb()
	// 1 - (3/4)^3
	if failure.Cmp(rat("37/64")) != 0 {
		t.Fatalf("failure = %s, want 37/64", failure)
	}
}
