package wafdecision

import "testing"

func TestZeroCostLeaves(t *testing.T) {
	rule := AndOf(NewLeaf(0), OrOf(NewLeaf(1), NewLeaf(2)))
	specs := []LeafSpec{
		spec(0, 0, 0.5, 0.5),
		spec(1, 0, 0.5, 0.5),
		spec(2, 0, 0.5, 0.5),
	}
	g, err := Compile(rule, specs)
	if err != nil {
		t.Fatal(err)
	}
	if g.WorstCost != 0 || g.MidExpectedCost != 0 {
		t.Fatalf("zero-cost rule: worst=%d mid=%v", g.WorstCost, g.MidExpectedCost)
	}
	rep, err := g.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.MaxObservedCost != 0 {
		t.Fatalf("observed cost = %d", rep.MaxObservedCost)
	}
}

func TestDeepNestedNegation(t *testing.T) {
	// !!a AND !(b OR !c) == a AND !b AND c
	rule := AndOf(
		NotOf(NotOf(NewLeaf(0))),
		NotOf(OrOf(NewLeaf(1), NotOf(NewLeaf(2)))),
	)
	specs := []LeafSpec{
		spec(0, 2, 0.3, 0.3),
		spec(1, 4, 0.6, 0.6),
		spec(2, 6, 0.9, 0.9),
	}
	g, err := Compile(rule, specs)
	if err != nil {
		t.Fatal(err)
	}
	for mask := 0; mask < 8; mask++ {
		a := map[int]bool{
			0: mask&1 != 0,
			1: mask&2 != 0,
			2: mask&4 != 0,
		}
		det, calls := map[int]Detector{}, map[int]int{}
		for id := range a {
			det[id] = detectorStub(a, calls, id)
		}
		v, _ := g.Eval(det)
		want, _ := EvalFormula(rule, a)
		if (v == Deny) != want {
			t.Fatalf("mask=%b: got %v want %v", mask, v == Deny, want)
		}
	}
	if _, err := g.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSingleLeaf(t *testing.T) {
	g, err := Compile(NewLeaf(0), []LeafSpec{spec(0, 9, 0.2, 0.4)})
	if err != nil {
		t.Fatal(err)
	}
	if g.WorstCost != 9 || g.LeafCount != 1 {
		t.Fatalf("worst=%d leaves=%d", g.WorstCost, g.LeafCount)
	}
	rep, err := g.Verify()
	if err != nil || rep.Assignments != 2 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
	out := g.Decide(map[int]Detector{0: func() bool { return true }})
	if out.Verdict != Deny {
		t.Fatalf("true leaf should deny")
	}
}
