package wafdecision

import "testing"

func spec(id int, cost uint64, lo, hi float64) LeafSpec {
	return LeafSpec{ID: id, Cost: cost, SelLo: lo, SelHi: hi}
}

func TestCompileValidation(t *testing.T) {
	leaf := NewLeaf(0)

	if _, err := Compile(nil, []LeafSpec{spec(0, 1, 0, 1)}); err != ErrNilFormula {
		t.Fatalf("nil formula: got %v", err)
	}
	if _, err := Compile(leaf, nil); err != ErrNoLeaves {
		t.Fatalf("no specs: got %v", err)
	}
	big := make([]LeafSpec, MaxLeaves+1)
	for i := range big {
		big[i] = spec(i, 1, 0, 1)
	}
	if _, err := Compile(leaf, big); err != ErrTooManyLeaves {
		t.Fatalf("too many leaves: got %v", err)
	}
	if _, err := Compile(leaf, []LeafSpec{spec(0, 1, 0.7, 0.2)}); err == nil {
		t.Fatal("expected inverted interval error")
	}
	if _, err := Compile(leaf, []LeafSpec{spec(0, 1, -0.1, 0.2)}); err == nil {
		t.Fatal("expected negative selectivity error")
	}
	if _, err := Compile(leaf, []LeafSpec{
		spec(0, 1, 0, 1),
		spec(1, 1, 0, 1),
	}); err == nil {
		t.Fatal("expected unreferenced leaf error")
	}
	if _, err := Compile(AndOf(NewLeaf(0), NewLeaf(1)), []LeafSpec{
		spec(0, 1, 0, 1),
	}); err == nil {
		t.Fatal("expected missing spec error")
	}
	if _, err := Compile(leaf, []LeafSpec{
		spec(0, 1, 0, 1),
		spec(0, 2, 0, 1),
	}); err == nil {
		t.Fatal("expected duplicate identifier error")
	}
}

func TestWorstCostConjunction(t *testing.T) {
	// Pure conjunction: every leaf runs only on the all-true path, so the
	// worst-case cost is the full sum regardless of ordering.
	formula := AndOf(NewLeaf(0), NewLeaf(1), NewLeaf(2))
	specs := []LeafSpec{
		spec(0, 5, 0.9, 0.9),
		spec(1, 3, 0.1, 0.1),
		spec(2, 9, 0.1, 0.1),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if g.WorstCost != 17 {
		t.Fatalf("worst cost = %d, want 17", g.WorstCost)
	}
	// Among worst-cost-equal orderings, the leaf most likely to short-circuit
	// (low selectivity in a conjunction) wins the expected-cost tie-breaker.
	if g.nodes[g.Root()].Kind != NodeTest || g.nodes[g.Root()].Leaf != 1 {
		t.Fatalf("root leaf = %d, want 1", g.nodes[g.Root()].Leaf)
	}
}

func TestWorstCostDisjunction(t *testing.T) {
	// Pure disjunction: every leaf runs only on the all-false path, so the
	// worst-case cost is again the full sum.
	formula := OrOf(NewLeaf(0), NewLeaf(1))
	specs := []LeafSpec{
		spec(0, 4, 0.9, 0.95),
		spec(1, 8, 0.2, 0.3),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if g.WorstCost != 12 {
		t.Fatalf("worst cost = %d, want 12", g.WorstCost)
	}
	// High selectivity makes leaf 0 the better first probe at equal worst cost.
	if g.nodes[0].Leaf != 0 {
		t.Fatalf("root leaf = %d, want 0", g.nodes[0].Leaf)
	}
}

func TestSharedLeafEvaluatedOnce(t *testing.T) {
	// (a AND b) OR (a AND c): leaf a is shared and must run at most once.
	formula := OrOf(
		AndOf(NewLeaf(0), NewLeaf(1)),
		AndOf(NewLeaf(0), NewLeaf(2)),
	)
	specs := []LeafSpec{
		spec(0, 10, 0.5, 0.5),
		spec(1, 1, 0.5, 0.5),
		spec(2, 1, 0.5, 0.5),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	calls := map[int]int{}
	det := map[int]Detector{}
	for _, s := range specs {
		det[s.ID] = func(id int) Detector {
			return func() bool {
				calls[id]++
				return true
			}
		}(s.ID)
	}
	v, st := g.Eval(det)
	if v != Deny {
		t.Fatalf("all true should deny, got %v", v)
	}
	if calls[0] != 1 {
		t.Fatalf("shared leaf called %d times, want 1", calls[0])
	}
	if st.CallCount != 2 {
		t.Fatalf("call count = %d, want 2", st.CallCount)
	}
}

func TestNegationSemantics(t *testing.T) {
	formula := AndOf(NewLeaf(0), NotOf(NewLeaf(1)))
	specs := []LeafSpec{
		spec(0, 2, 0.5, 0.5),
		spec(1, 3, 0.5, 0.5),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		a, b bool
		want Verdict
	}{
		{false, false, Allow},
		{true, false, Deny},
		{false, true, Allow},
		{true, true, Allow},
	}
	for _, tc := range cases {
		det := map[int]Detector{
			0: func() bool { return tc.a },
			1: func() bool { return tc.b },
		}
		got, _ := g.Eval(det)
		if got != tc.want {
			t.Fatalf("a=%v b=%v: got %v want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPanicBecomesDeny(t *testing.T) {
	formula := OrOf(NewLeaf(0), AndOf(NewLeaf(1), NewLeaf(2)))
	specs := []LeafSpec{
		spec(0, 5, 0.1, 0.2),
		spec(1, 7, 0.1, 0.2),
		spec(2, 2, 0.1, 0.2),
	}
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	det := map[int]Detector{
		0: func() bool { return false },
		1: func() bool { panic("backend down") },
		2: func() bool { return true },
	}
	out := g.Decide(det)
	if out.Verdict != Deny || !out.Panicked || out.PanicLeaf != 1 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if out.Stats.TotalCost != 12 {
		t.Fatalf("partial cost = %d, want 12", out.Stats.TotalCost)
	}
	if out.PanicValue != "backend down" {
		t.Fatalf("panic value = %v", out.PanicValue)
	}

	// Eval without recovery must propagate the panic instead.
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic propagation from Eval")
		}
	}()
	_, _ = g.Eval(det)
}

func TestTwelveLeavesStress(t *testing.T) {
	children := make([]Formula, MaxLeaves)
	specs := make([]LeafSpec, MaxLeaves)
	for i := 0; i < MaxLeaves; i++ {
		children[i] = NewLeaf(i)
		specs[i] = spec(i, uint64(i+1), float64(i)/12.0, float64(i+1)/12.0)
	}
	formula := OrOf(
		AndOf(children[0:4]...),
		AndOf(children[4:8]...),
		OrOf(children[8:12]...),
	)
	g, err := Compile(formula, specs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rep, err := g.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Assignments != 1<<MaxLeaves {
		t.Fatalf("assignments = %d", rep.Assignments)
	}
	if rep.MaxCalls > MaxLeaves {
		t.Fatalf("max calls = %d", rep.MaxCalls)
	}
	var sum uint64
	for _, s := range specs {
		sum += s.Cost
	}
	if rep.MaxObservedCost > sum {
		t.Fatalf("observed cost %d exceeds total leaf cost %d", rep.MaxObservedCost, sum)
	}
	t.Logf("12-leaf graph: nodes=%d states=%d worst=%d E[lo=%v hi=%v]",
		len(g.nodes), g.StatesVisited, g.WorstCost, rep.ExpectedCostLo, rep.ExpectedCostHi)
}
