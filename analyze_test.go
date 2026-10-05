package main

import "testing"

func fullDomain() []int8 {
	vals := make([]int8, 0, 256)
	for v := -128; v <= 127; v++ {
		vals = append(vals, int8(v))
	}
	return vals
}

func smallDomain() []int8 {
	vals := make([]int8, 0, 16)
	for v := -8; v <= 7; v++ {
		vals = append(vals, int8(v))
	}
	return vals
}

func byStatus(results []Result) map[Status][]Result {
	m := map[Status][]Result{Killed: {}, Survived: {}, Equivalent: {}}
	for _, r := range results {
		m[r.Status] = append(m[r.Status], r)
	}
	return m
}

// x - 0 mutated to x + 0 is equivalent on the whole int8 domain.
func TestEquivalentMutant(t *testing.T) {
	prog, err := parse("input a\nreturn a - 0\n")
	if err != nil {
		t.Fatal(err)
	}
	results, err := analyze(prog, nil, fullDomain())
	if err != nil {
		t.Fatal(err)
	}
	g := byStatus(results)
	if len(g[Equivalent]) == 0 {
		t.Fatal("expected the operator swap on (a - 0) to be equivalent")
	}
	found := false
	for _, r := range g[Equivalent] {
		if r.Mutant.Desc == "arithmetic (a - 0) -> (a + 0)" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing expected equivalent mutant; got %v", g[Equivalent])
	}
}

// With no tests, a behavior-changing mutant survives and must come
// with a concrete counterexample inside the domain.
func TestSurvivedMutantHasCounterexample(t *testing.T) {
	prog, err := parse("input a, b\nreturn a + b\n")
	if err != nil {
		t.Fatal(err)
	}
	results, err := analyze(prog, nil, smallDomain())
	if err != nil {
		t.Fatal(err)
	}
	g := byStatus(results)
	if len(g[Survived]) == 0 {
		t.Fatal("expected survived mutants")
	}
	for _, r := range g[Survived] {
		if r.Witness == nil {
			t.Errorf("survived mutant %s has no counterexample", r.Mutant.Desc)
			continue
		}
		// Verify the counterexample actually distinguishes the programs.
		mp := applyMutant(prog, r.Mutant)
		ov, _ := run(prog, r.Witness)
		mv, _ := run(mp, r.Witness)
		if ov == mv {
			t.Errorf("counterexample %v does not distinguish %s", r.Witness, r.Mutant.Desc)
		}
		if ov != r.Orig || mv != r.Mut {
			t.Errorf("recorded outputs (%d,%d) disagree with rerun (%d,%d)",
				r.Orig, r.Mut, ov, mv)
		}
	}
}

// A test that distinguishes the mutant kills it.
func TestKilledMutant(t *testing.T) {
	prog, err := parse("input a, b\nreturn a + b\n")
	if err != nil {
		t.Fatal(err)
	}
	tests := []map[string]int8{{"a": 1, "b": 1}}
	results, err := analyze(prog, tests, smallDomain())
	if err != nil {
		t.Fatal(err)
	}
	g := byStatus(results)
	found := false
	for _, r := range g[Killed] {
		if r.Mutant.Desc == "arithmetic (a + b) -> (a - b)" {
			found = true
			if r.Orig != 2 || r.Mut != 0 {
				t.Errorf("killing outputs: got orig=%d mut=%d, want 2,0", r.Orig, r.Mut)
			}
		}
	}
	if !found {
		t.Error("expected (a + b) -> (a - b) to be killed by test a=1,b=1")
	}
}

// Comparison operator mutants: a < b vs a <= b differ only at a == b,
// so a suite without a diagonal test leaves the mutant alive.
func TestBoundaryMutantSurvivesWithoutDiagonalTest(t *testing.T) {
	prog, err := parse("input a, b\nif a < b\n  return 1\nelse\n  return 0\nend\n")
	if err != nil {
		t.Fatal(err)
	}
	tests := []map[string]int8{{"a": 1, "b": 2}, {"a": 2, "b": 1}}
	results, err := analyze(prog, tests, smallDomain())
	if err != nil {
		t.Fatal(err)
	}
	g := byStatus(results)
	var boundary *Result
	for i := range g[Survived] {
		if g[Survived][i].Mutant.Desc == "comparison (a < b) -> (a <= b)" {
			boundary = &g[Survived][i]
		}
	}
	if boundary == nil {
		t.Fatal("expected (a < b) -> (a <= b) to survive")
	}
	if boundary.Witness["a"] != boundary.Witness["b"] {
		t.Errorf("counterexample should be on the diagonal, got %v", boundary.Witness)
	}
	// Now add a diagonal test: the mutant must die.
	tests = append(tests, map[string]int8{"a": 3, "b": 3})
	results, err = analyze(prog, tests, smallDomain())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range byStatus(results)[Killed] {
		if r.Mutant.Desc == "comparison (a < b) -> (a <= b)" {
			return
		}
	}
	t.Error("expected (a < b) -> (a <= b) to be killed once a diagonal test exists")
}

// Wraparound is part of the semantics: the constant mutant 127 -> 127+1
// must produce -128, not clamp or error.
func TestConstantMutantWraps(t *testing.T) {
	prog, err := parse("input a\nreturn a + 127\n")
	if err != nil {
		t.Fatal(err)
	}
	results, err := analyze(prog, nil, fullDomain())
	if err != nil {
		t.Fatal(err)
	}
	// a+127 vs a+(-128): 127 != -128 (mod 256), so the mutant changes
	// behavior and survives with a counterexample.
	g := byStatus(results)
	found := false
	for _, r := range g[Survived] {
		if r.Mutant.Desc == "constant 127 -> -128" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected mutant [constant 127 -> -128] among survivors: %v", g[Survived])
	}
}

// applyMutant must not modify the original program (immutability).
func TestApplyMutantDoesNotClobberOriginal(t *testing.T) {
	prog, err := parse("input a\nreturn a + 1\n")
	if err != nil {
		t.Fatal(err)
	}
	before := runSrc(t, "input a\nreturn a + 1\n", map[string]int8{"a": 41})
	for _, m := range genMutants(prog) {
		applyMutant(prog, m)
	}
	after, err := run(prog, map[string]int8{"a": 41})
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("original program changed by applyMutant: %d != %d", before, after)
	}
}
