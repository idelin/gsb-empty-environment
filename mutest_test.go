package main

import "testing"

func mustParse(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return p
}

func mustRun(t *testing.T, p *Program, in ...int8) interface{} {
	t.Helper()
	v, err := Run(p, in)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	return v
}

func smallDomain(n int) Domain {
	sets := make([][]int8, n)
	for i := range sets {
		sets[i] = boundarySet
	}
	return NewDomain(sets)
}

func TestWrapArithmetic(t *testing.T) {
	p := mustParse(t, "input a\nreturn a + 1")
	if got := mustRun(t, p, 127); got != int8(-128) {
		t.Fatalf("127 + 1 = %v, want -128", got)
	}
	p = mustParse(t, "input a\nreturn a - 1")
	if got := mustRun(t, p, -128); got != int8(127) {
		t.Fatalf("-128 - 1 = %v, want 127", got)
	}
	p = mustParse(t, "return 100 + 100")
	if got := mustRun(t, p); got != int8(-56) {
		t.Fatalf("100 + 100 = %v, want -56", got)
	}
	p = mustParse(t, "return -128")
	if got := mustRun(t, p); got != int8(-128) {
		t.Fatalf("-128 literal = %v, want -128", got)
	}
}

func TestBranchAndCompare(t *testing.T) {
	p := mustParse(t, `
input a, b
if a < b {
    r = a + b
} else {
    r = a - b
}
return r
`)
	if got := mustRun(t, p, 1, 2); got != int8(3) {
		t.Fatalf("got %v, want 3", got)
	}
	if got := mustRun(t, p, 5, 2); got != int8(3) {
		t.Fatalf("got %v, want 3", got)
	}
}

func TestTypeError(t *testing.T) {
	p := mustParse(t, "input a\nreturn a + (a < 1)")
	if _, err := Run(p, []int8{0}); err == nil {
		t.Fatal("expected type error, got nil")
	}
}

func TestMutantCount(t *testing.T) {
	p := mustParse(t, `
input a, b
if a < b {
    r = a + b
} else {
    r = a - 1
}
return r
`)
	// 1 cmp -> 5 ROR, 2 arith -> 2 AOR, 1 const -> 2 CRR, 1 if -> 1 CND
	ms := GenerateMutants(p)
	if len(ms) != 10 {
		t.Fatalf("got %d mutants, want 10", len(ms))
	}
	counts := map[string]int{}
	for _, m := range ms {
		counts[m.Op]++
	}
	want := map[string]int{"ROR": 5, "AOR": 2, "CRR": 2, "CND": 1}
	for op, n := range want {
		if counts[op] != n {
			t.Fatalf("%s: got %d, want %d", op, counts[op], n)
		}
	}
}

func TestClassifyKilled(t *testing.T) {
	p := mustParse(t, `
input a, b
if a < b {
    r = a + b
} else {
    r = a - b
}
return r
`)
	ms := GenerateMutants(p)
	rep := Classify(p, ms, [][]int8{{1, 2}}, smallDomain(2))
	// Test (1,2) takes the then branch: it kills CND, ROR to ==/>/>=, and
	// AOR on a+b. It cannot kill ROR to !=/<= (same on this test) or AOR on
	// the unexecuted else branch; those survive with counterexamples.
	if len(rep.Killed) != 5 {
		t.Fatalf("killed %d of %d mutants, want 5", len(rep.Killed), len(ms))
	}
	if len(rep.Equivalent) != 0 {
		t.Fatalf("equivalent %d, want 0", len(rep.Equivalent))
	}
	if len(rep.Survived) != 3 {
		t.Fatalf("survived %d, want 3", len(rep.Survived))
	}
	for _, s := range rep.Survived {
		ov, _ := Run(p, s.Counterexample)
		mv, _ := Run(s.M.Prog, s.Counterexample)
		if ov == mv {
			t.Fatalf("counterexample %v does not distinguish (both %v)", s.Counterexample, ov)
		}
	}
}

func TestClassifyEquivalent(t *testing.T) {
	// Both branches identical: any condition mutation is equivalent.
	p := mustParse(t, `
input a
if a < 1 {
    return 1
} else {
    return 1
}
`)
	ms := GenerateMutants(p)
	rep := Classify(p, ms, nil, smallDomain(1))
	if len(rep.Killed) != 0 {
		t.Fatalf("killed %d, want 0", len(rep.Killed))
	}
	// 5 ROR + 1 CND + 2 CRR on the condition constant are equivalent;
	// 2 CRR on each of the two return constants (1->2, 1->0) survive.
	if len(rep.Equivalent) != 8 {
		t.Fatalf("equivalent %d, want 8", len(rep.Equivalent))
	}
	if len(rep.Survived) != 4 {
		t.Fatalf("survived %d, want 4", len(rep.Survived))
	}
	for _, s := range rep.Survived {
		if s.M.Op != "CRR" {
			t.Fatalf("unexpected surviving op %s", s.M.Op)
		}
		// The counterexample must actually distinguish the two programs.
		ov, _ := Run(p, s.Counterexample)
		mv, _ := Run(s.M.Prog, s.Counterexample)
		if ov == mv {
			t.Fatalf("counterexample %v does not distinguish (both %v)", s.Counterexample, ov)
		}
	}
}

func TestClassifySurvivingCounterexample(t *testing.T) {
	p := mustParse(t, "input a, b\nreturn a - b")
	ms := GenerateMutants(p)
	if len(ms) != 1 {
		t.Fatalf("got %d mutants, want 1", len(ms))
	}
	rep := Classify(p, ms, [][]int8{{3, 0}}, smallDomain(2)) // test kills nothing: 3-0 == 3+0
	if len(rep.Survived) != 1 {
		t.Fatalf("survived %d, want 1", len(rep.Survived))
	}
	ce := rep.Survived[0].Counterexample
	ov, _ := Run(p, ce)
	mv, _ := Run(ms[0].Prog, ce)
	if ov == mv {
		t.Fatalf("counterexample %v does not distinguish", ce)
	}
	// With a killing test the mutant must be caught instead.
	rep = Classify(p, ms, [][]int8{{1, 1}}, smallDomain(2))
	if len(rep.Killed) != 1 {
		t.Fatalf("killed %d, want 1", len(rep.Killed))
	}
}

func TestDomainEnumeration(t *testing.T) {
	d := NewDomain([][]int8{{-1, 0}, {10, 20, 30}})
	if d.Total() != 6 {
		t.Fatalf("total %d, want 6", d.Total())
	}
	buf := make([]int8, 2)
	seen := map[[2]int8]bool{}
	for i := int64(0); i < d.Total(); i++ {
		d.Fill(i, buf)
		seen[[2]int8{buf[0], buf[1]}] = true
	}
	if len(seen) != 6 {
		t.Fatalf("enumerated %d distinct vectors, want 6", len(seen))
	}
}
