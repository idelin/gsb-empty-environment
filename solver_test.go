package main

import "testing"

func solve(t *testing.T, p *Problem) *Solution {
	t.Helper()
	if err := p.Validate(); err != nil {
		t.Fatalf("invalid problem: %v", err)
	}
	return Solve(p)
}

func concatOf(p *Problem, assignment map[string]string) string {
	out := ""
	for _, v := range p.Variables {
		out += assignment[v.Name]
	}
	return out
}

// Each variable has length 1, so the forbidden "aa" can only arise across
// the variable boundary; the solver must reject X=a,Y=a.
func TestCrossBoundaryForbidden(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Variables: []VarSpec{
			{Name: "X", MinLen: 1, MaxLen: 1},
			{Name: "Y", MinLen: 1, MaxLen: 1},
		},
		Forbidden: []string{"aa"},
	}
	sol := solve(t, p)
	if sol.Status != StatusSAT {
		t.Fatalf("status = %s, want SAT", sol.Status)
	}
	if got := concatOf(p, sol.Assignment); got != "ab" {
		t.Fatalf("concat = %q, want %q", got, "ab")
	}
	if err := Verify(p, sol.Assignment); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The boundary violation must be caught by the independent checker even
	// though each variable individually satisfies its own constraints.
	bad := map[string]string{"X": "a", "Y": "a"}
	if err := Verify(p, bad); err == nil {
		t.Fatal("Verify accepted a cross-boundary forbidden substring")
	}
}

func TestConcatEquality(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Variables: []VarSpec{
			{Name: "X", MinLen: 1, MaxLen: 2},
			{Name: "Y", MinLen: 0, MaxLen: 2},
			{Name: "Z", MinLen: 1, MaxLen: 2},
			{Name: "W", MinLen: 0, MaxLen: 2},
		},
		Equalities: []Equality{{Left: []string{"X", "Y"}, Right: []string{"Z", "W"}}},
	}
	sol := solve(t, p)
	if sol.Status != StatusSAT {
		t.Fatalf("status = %s, want SAT", sol.Status)
	}
	if sol.TotalLength != 2 {
		t.Fatalf("total length = %d, want 2", sol.TotalLength)
	}
	want := map[string]string{"X": "a", "Y": "", "Z": "a", "W": ""}
	for name, val := range want {
		if sol.Assignment[name] != val {
			t.Fatalf("assignment[%q] = %q, want %q (full: %v)", name, sol.Assignment[name], val, sol.Assignment)
		}
	}
	if err := Verify(p, sol.Assignment); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPrefixSuffixOverlap(t *testing.T) {
	p := &Problem{
		Alphabet:  "ab",
		Variables: []VarSpec{{Name: "X", MinLen: 2, MaxLen: 5, Prefix: "ab", Suffix: "ba"}},
	}
	sol := solve(t, p)
	if sol.Status != StatusSAT {
		t.Fatalf("status = %s, want SAT", sol.Status)
	}
	// "ab" and "ba" overlap into "aba" of length 3.
	if sol.Assignment["X"] != "aba" {
		t.Fatalf("X = %q, want %q", sol.Assignment["X"], "aba")
	}
}

func TestPrefixSuffixInfeasible(t *testing.T) {
	p := &Problem{
		Alphabet:  "ab",
		Variables: []VarSpec{{Name: "X", MinLen: 0, MaxLen: 2, Prefix: "ab", Suffix: "ba"}},
	}
	if sol := solve(t, p); sol.Status != StatusUNSAT {
		t.Fatalf("status = %s, want UNSAT", sol.Status)
	}
}

func TestUnsatForbidden(t *testing.T) {
	p := &Problem{
		Alphabet:  "a",
		Variables: []VarSpec{{Name: "X", MinLen: 1, MaxLen: 2}},
		Forbidden: []string{"a"},
	}
	if sol := solve(t, p); sol.Status != StatusUNSAT {
		t.Fatalf("status = %s, want UNSAT", sol.Status)
	}
}

func TestAlphabetOrderDefinesLex(t *testing.T) {
	p := &Problem{
		Alphabet: "ba", // b is smaller than a
		Variables: []VarSpec{
			{Name: "X", MinLen: 1, MaxLen: 1},
			{Name: "Y", MinLen: 1, MaxLen: 1},
		},
	}
	sol := solve(t, p)
	if got := concatOf(p, sol.Assignment); got != "bb" {
		t.Fatalf("concat = %q, want %q", got, "bb")
	}
}

// Budget counts search states; exhausting it must yield UNKNOWN, never UNSAT.
func TestBudgetSemantics(t *testing.T) {
	base := &Problem{
		Alphabet: "ab",
		Variables: []VarSpec{
			{Name: "X", MinLen: 1, MaxLen: 3},
			{Name: "Y", MinLen: 1, MaxLen: 3},
		},
		Forbidden: []string{"aa", "bb"},
	}
	full := solve(t, base)
	if full.Status != StatusSAT {
		t.Fatalf("status = %s, want SAT", full.Status)
	}
	if full.States <= 1 {
		t.Fatalf("states = %d, want a nontrivial search", full.States)
	}

	exact := *base
	exact.Budget = full.States
	sol := solve(t, &exact)
	if sol.Status != StatusSAT || concatOf(&exact, sol.Assignment) != concatOf(base, full.Assignment) {
		t.Fatalf("exact budget: status = %s, want SAT with same optimum", sol.Status)
	}

	short := *base
	short.Budget = full.States - 1
	sol = solve(t, &short)
	if sol.Status != StatusUNKNOWN {
		t.Fatalf("tight budget: status = %s, want UNKNOWN", sol.Status)
	}

	// An UNSAT instance with a tiny budget must report UNKNOWN, not UNSAT.
	unsat := &Problem{
		Alphabet:  "a",
		Variables: []VarSpec{{Name: "X", MinLen: 1, MaxLen: 3}},
		Forbidden: []string{"a"},
		Budget:    1,
	}
	if sol := solve(t, unsat); sol.Status != StatusUNKNOWN {
		t.Fatalf("status = %s, want UNKNOWN (search incomplete)", sol.Status)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Variables: []VarSpec{
			{Name: "X", MinLen: 1, MaxLen: 2},
			{Name: "Z", MinLen: 1, MaxLen: 2},
		},
		Equalities: []Equality{{Left: []string{"X"}, Right: []string{"Z"}}},
	}
	sol := solve(t, p)
	if sol.Status != StatusSAT {
		t.Fatalf("status = %s, want SAT", sol.Status)
	}
	bad := map[string]string{"X": sol.Assignment["X"], "Z": sol.Assignment["Z"] + "b"}
	if err := Verify(p, bad); err == nil {
		t.Fatal("Verify accepted a broken equality")
	}
	bad = map[string]string{"X": "c", "Z": "c"}
	if err := Verify(p, bad); err == nil {
		t.Fatal("Verify accepted characters outside the alphabet")
	}
}
