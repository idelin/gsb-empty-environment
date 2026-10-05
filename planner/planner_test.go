package planner

import "testing"

// diamondSpec: a is shared by b and c; d depends on both.
//
//	a(5/10/4) <-- b(3/6/2), c(3/6/2) <-- d(2/4/8)
func diamondSpec() Spec {
	return Spec{Blocks: []Block{
		{ID: "a", Compressed: 5, Temp: 10, Final: 4},
		{ID: "b", Compressed: 3, Temp: 6, Final: 2, Deps: []string{"a"}},
		{ID: "c", Compressed: 3, Temp: 6, Final: 2, Deps: []string{"a"}},
		{ID: "d", Compressed: 2, Temp: 4, Final: 8, Deps: []string{"b", "c"}},
	}}
}

func mustNew(t *testing.T, spec Spec) *Planner {
	t.Helper()
	p, err := New(spec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestEvalSharedBlockCountedOnce(t *testing.T) {
	p := mustNew(t, diamondSpec())
	// Order a,b,c,d, replayed by hand:
	//  a: 0+5+10=15 (peak) -> publish -> 4
	//  b: 4+3+6=13          -> publish -> 6   (a still live: c unpublished)
	//  c: 6+3+6=15 (peak)   -> publish -> 8   (a's LAST dependent done: -4 -> 4)
	//  d: 4+2+4=10          -> publish -> 12  (b,c released: -4 -> 8)
	// a's 4 final bytes are counted once even though b and c both need them.
	peak, err := p.Eval([]int{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if peak != 15 {
		t.Fatalf("peak = %d, want 15", peak)
	}
}

func TestPeakIsNotFinalSize(t *testing.T) {
	p := mustNew(t, diamondSpec())
	peak, err := p.Eval([]int{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	var finalSum int64
	for _, b := range diamondSpec().Blocks {
		finalSum += b.Final
	}
	if peak == finalSum {
		t.Fatalf("peak %d must not be equated with total final size %d", peak, finalSum)
	}
	if finalSum != 16 || peak != 15 {
		t.Fatalf("finalSum=%d peak=%d, want 16 and 15", finalSum, peak)
	}
}

func TestEvalRejectsBadOrders(t *testing.T) {
	p := mustNew(t, diamondSpec())
	if _, err := p.Eval([]int{1, 0, 2, 3}); err == nil {
		t.Fatal("expected error: b before its dependency a")
	}
	if _, err := p.Eval([]int{0, 1, 2}); err == nil {
		t.Fatal("expected error: short order")
	}
	if _, err := p.Eval([]int{0, 0, 1, 2}); err == nil {
		t.Fatal("expected error: repeated block")
	}
}

func TestLowerBound(t *testing.T) {
	p := mustNew(t, diamondSpec())
	// per-block: a:15, b:3+6+4=13, c:13, d:2+4+2+2=10; outputs end: 8.
	if lb := p.LowerBound(); lb != 15 {
		t.Fatalf("LowerBound = %d, want 15", lb)
	}
}

func TestValidation(t *testing.T) {
	cases := []Spec{
		{Blocks: []Block{}},
		{Blocks: []Block{{ID: "a"}, {ID: "a"}}},
		{Blocks: []Block{{ID: "a", Compressed: -1}}},
		{Blocks: []Block{{ID: "a", Deps: []string{"ghost"}}}},
		{Blocks: []Block{{ID: "a", Deps: []string{"a"}}}},
		{Blocks: []Block{
			{ID: "a", Deps: []string{"b"}},
			{ID: "b", Deps: []string{"a"}},
		}},
		{Blocks: []Block{{ID: "a"}}, Outputs: []string{"ghost"}},
	}
	for k, spec := range cases {
		if _, err := New(spec); err == nil {
			t.Fatalf("case %d: expected validation error", k)
		}
	}
}

func TestDuplicateDepCountsOnce(t *testing.T) {
	p := mustNew(t, Spec{Blocks: []Block{
		{ID: "a", Compressed: 1, Temp: 1, Final: 7},
		{ID: "b", Compressed: 1, Temp: 1, Final: 1, Deps: []string{"a", "a"}},
	}})
	// b's decompress sees a's final exactly once: 1+1+7 = 9.
	if lb := p.LowerBound(); lb != 9 {
		t.Fatalf("LowerBound = %d, want 9", lb)
	}
}

func TestPeakAtPublishPoint(t *testing.T) {
	// final exceeds compressed+temp: usage peaks at the publish instant,
	// not during decompress. Regression test for publish-point tracking.
	p := mustNew(t, Spec{Blocks: []Block{
		{ID: "a", Compressed: 12, Temp: 6, Final: 11},
		{ID: "b", Compressed: 8, Temp: 1, Final: 11},
		{ID: "c", Compressed: 5, Temp: 1, Final: 4},
	}})
	peak, err := p.Eval([]int{2, 0, 1})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if peak != 26 {
		t.Fatalf("peak = %d, want 26 (end state 11+11+4)", peak)
	}
}
