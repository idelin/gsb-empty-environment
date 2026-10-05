package main

import "testing"

func le(coeffs map[string]int64, k int64) Constraint {
	return Constraint{Coeffs: coeffs, Op: "<=", Const: k}
}
func ge(coeffs map[string]int64, k int64) Constraint {
	return Constraint{Coeffs: coeffs, Op: ">=", Const: k}
}
func eq(coeffs map[string]int64, k int64) Constraint {
	return Constraint{Coeffs: coeffs, Op: "==", Const: k}
}

// x starts at 0, loops while x < 10 doing x := x + 1.
func counterProgram(inv []Constraint, post []Constraint) *Program {
	return &Program{
		Variables: []string{"x"},
		Bounds:    Bounds{Min: -20, Max: 20},
		Init:      []Constraint{eq(map[string]int64{"x": 1}, 0)},
		Guard:     []Constraint{Constraint{Coeffs: map[string]int64{"x": 1}, Op: "<", Const: 10}},
		Assign:    []Assignment{{Var: "x", Coeffs: map[string]int64{"x": 1}, Const: 1}},
		Invariant: inv,
		Post:      post,
	}
}

func TestProved(t *testing.T) {
	p := counterProgram(
		[]Constraint{ge(map[string]int64{"x": 1}, 0), le(map[string]int64{"x": 1}, 10)},
		[]Constraint{eq(map[string]int64{"x": 1}, 10)},
	)
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Proved {
		t.Fatalf("verdict = %s, want PROVED (report: %+v)", rep.Verdict, rep)
	}
	if !rep.Init.Holds || !rep.Preservation.Holds || !rep.Post.Holds {
		t.Fatalf("expected all conditions to hold: %+v", rep)
	}
	if rep.OutOfBoundsCount != 0 {
		t.Fatalf("unexpected out-of-bounds transitions: %d", rep.OutOfBoundsCount)
	}
}

func TestInitViolation(t *testing.T) {
	p := counterProgram(
		[]Constraint{ge(map[string]int64{"x": 1}, 1)}, // false at x=0
		[]Constraint{ge(map[string]int64{"x": 1}, 0)},
	)
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Failed || rep.Init.Holds {
		t.Fatalf("verdict = %s, init.Holds = %v; want FAILED with init violation", rep.Verdict, rep.Init.Holds)
	}
	if len(rep.Init.Counterexamples) == 0 || rep.Init.Counterexamples[0]["x"] != 0 {
		t.Fatalf("missing/wrong init counterexample: %+v", rep.Init.Counterexamples)
	}
}

func TestPreservationViolation(t *testing.T) {
	// Invariant x <= 9 is not preserved: at x=9 the step yields x=10.
	p := counterProgram(
		[]Constraint{ge(map[string]int64{"x": 1}, 0), le(map[string]int64{"x": 1}, 9)},
		[]Constraint{ge(map[string]int64{"x": 1}, 0)},
	)
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Failed || rep.Preservation.Holds {
		t.Fatalf("verdict = %s, preservation.Holds = %v; want FAILED", rep.Verdict, rep.Preservation.Holds)
	}
	tr := rep.Preservation.Transitions[0]
	if tr.Pre["x"] != 9 || tr.Post["x"] != 10 {
		t.Fatalf("wrong counterexample transition: %v", tr)
	}
}

func TestPostViolation(t *testing.T) {
	// Post x >= 11 does not follow: at x = 10 the invariant holds and the
	// guard is false, but the postcondition fails.
	p := counterProgram(
		[]Constraint{le(map[string]int64{"x": 1}, 10)},
		[]Constraint{ge(map[string]int64{"x": 1}, 11)},
	)
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Failed || rep.Post.Holds {
		t.Fatalf("verdict = %s, post.Holds = %v; want FAILED", rep.Verdict, rep.Post.Holds)
	}
	if len(rep.Post.Counterexamples) == 0 {
		t.Fatal("missing post counterexample")
	}
	ce := rep.Post.Counterexamples[0]
	if ce["x"] != 10 {
		t.Fatalf("post counterexample should satisfy inv and !guard, got %v", ce)
	}
}

func TestOutOfBoundsIsIndeterminate(t *testing.T) {
	// Range ends at 10; at x=10 the guard holds and the step leaves the range.
	// No in-range violation exists, but this must NOT be reported as PROVED.
	p := counterProgram(
		[]Constraint{ge(map[string]int64{"x": 1}, 0), le(map[string]int64{"x": 1}, 10)},
		[]Constraint{ge(map[string]int64{"x": 1}, 0)},
	)
	p.Bounds = Bounds{Min: 0, Max: 10}
	p.Guard = []Constraint{le(map[string]int64{"x": 1}, 10)}
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Indeterminate {
		t.Fatalf("verdict = %s, want INDETERMINATE", rep.Verdict)
	}
	if rep.OutOfBoundsCount == 0 || len(rep.OutOfBounds) == 0 {
		t.Fatal("expected out-of-bounds transitions to be listed")
	}
	tr := rep.OutOfBounds[0]
	if tr.Pre["x"] != 10 || tr.Post["x"] != 11 {
		t.Fatalf("wrong out-of-bounds transition: %v", tr)
	}
}

func TestParallelAssignment(t *testing.T) {
	// Swap x and y in parallel; invariant x + y == 5 must be preserved.
	p := &Program{
		Variables: []string{"x", "y"},
		Bounds:    Bounds{Min: -10, Max: 10},
		Init:      []Constraint{eq(map[string]int64{"x": 1}, 0), eq(map[string]int64{"y": 1}, 5)},
		Guard:     []Constraint{Constraint{Coeffs: map[string]int64{"x": 1}, Op: "<", Const: 3}},
		Assign: []Assignment{
			{Var: "x", Coeffs: map[string]int64{"y": 1}},
			{Var: "y", Coeffs: map[string]int64{"x": 1}},
		},
		Invariant: []Constraint{eq(map[string]int64{"x": 1, "y": 1}, 5)},
		Post:      []Constraint{ge(map[string]int64{"x": 1, "y": 1}, 5)},
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	rep, err := p.Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Preservation.Holds {
		t.Fatalf("parallel swap should preserve x+y==5: %+v", rep.Preservation.Transitions)
	}
}

func TestStateSpaceLimit(t *testing.T) {
	p := counterProgram(nil, nil)
	if _, err := p.Check(5); err == nil {
		t.Fatal("expected state-space limit error")
	}
}

func TestValidate(t *testing.T) {
	p := counterProgram(nil, nil)
	p.Assign = append(p.Assign, Assignment{Var: "x"})
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for double assignment")
	}
	p2 := counterProgram(nil, nil)
	p2.Guard = []Constraint{{Coeffs: map[string]int64{"z": 1}, Op: "<=", Const: 1}}
	if err := p2.Validate(); err == nil {
		t.Fatal("expected error for unknown variable")
	}
	p3 := counterProgram(nil, nil)
	p3.Guard = []Constraint{{Coeffs: map[string]int64{"x": 1}, Op: "=<", Const: 1}}
	if err := p3.Validate(); err == nil {
		t.Fatal("expected error for invalid op")
	}
}
