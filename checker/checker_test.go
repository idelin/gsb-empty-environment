package checker

import "testing"

func expr(const_ int64, coeffs map[string]int64) LinearExpr {
	return LinearExpr{Coeffs: coeffs, Const: const_}
}

func con(const_ int64, coeffs map[string]int64, op string) Constraint {
	return Constraint{Expr: expr(const_, coeffs), Op: op}
}

func mustValidate(t *testing.T, p *Program) {
	t.Helper()
	if err := p.Validate(1000000); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// counterLoop models: x := lo; while x < hi { x := x + step } with x bounded
// to [boundLo, boundHi].
func counterLoop(lo, hi, step, boundLo, boundHi int64, inv, post []Constraint) *Program {
	return &Program{
		Variables: []string{"x"},
		Bounds:    map[string][2]int64{"x": {boundLo, boundHi}},
		Init:      []Constraint{con(-lo, map[string]int64{"x": 1}, "==")},
		Guard:     []Constraint{con(hi, map[string]int64{"x": -1}, ">")},
		Body:      []Assignment{{Var: "x", Expr: expr(step, map[string]int64{"x": 1})}},
		Invariant: inv,
		Post:      post,
	}
}

func TestValidInvariant(t *testing.T) {
	// x := 0; while x < 10 { x := x + 1 }; assert x == 10.
	inv := []Constraint{
		con(0, map[string]int64{"x": 1}, ">="),
		con(10, map[string]int64{"x": -1}, ">="),
	}
	post := []Constraint{con(-10, map[string]int64{"x": 1}, "==")}
	p := counterLoop(0, 10, 1, 0, 10, inv, post)
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictValid {
		t.Fatalf("verdict = %s, want valid: %+v", rep.Verdict, rep)
	}
	if !rep.Init.Holds || !rep.Preservation.Holds || !rep.Sufficiency.Holds {
		t.Fatalf("expected all checks to hold: %+v", rep)
	}
}

func TestInitFails(t *testing.T) {
	// Invariant x >= 1 is false at the initial state x == 0.
	inv := []Constraint{con(-1, map[string]int64{"x": 1}, ">=")}
	post := []Constraint{con(-10, map[string]int64{"x": 1}, ">=")}
	p := counterLoop(0, 10, 1, 0, 10, inv, post)
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictInvalid {
		t.Fatalf("verdict = %s, want invalid", rep.Verdict)
	}
	if rep.Init.Holds {
		t.Fatal("init should not hold")
	}
	if rep.Init.Counterexample == nil || (*rep.Init.Counterexample)["x"] != 0 {
		t.Fatalf("init counterexample = %+v, want x=0", rep.Init.Counterexample)
	}
}

func TestPreservationFails(t *testing.T) {
	// Invariant x <= 9 is broken by the step x=9 -> x=10.
	inv := []Constraint{con(9, map[string]int64{"x": -1}, ">=")}
	post := []Constraint{con(-10, map[string]int64{"x": 1}, ">=")}
	p := counterLoop(0, 10, 1, 0, 10, inv, post)
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictInvalid {
		t.Fatalf("verdict = %s, want invalid", rep.Verdict)
	}
	if rep.Preservation.Holds {
		t.Fatal("preservation should not hold")
	}
	ce := rep.Preservation.Counterexample
	if ce == nil || ce.From["x"] != 9 || ce.To["x"] != "10" {
		t.Fatalf("preservation counterexample = %+v, want 9 -> 10", ce)
	}
}

func TestSufficiencyFails(t *testing.T) {
	// Invariant 0 <= x <= 10 holds, but does not imply post x == 10
	// (e.g. x == 5 satisfies invariant and not guard... use x==10? no:
	// invariant x <= 12 with bounds up to 12 gives exit states != 10).
	inv := []Constraint{
		con(0, map[string]int64{"x": 1}, ">="),
		con(12, map[string]int64{"x": -1}, ">="),
	}
	post := []Constraint{con(-10, map[string]int64{"x": 1}, "==")}
	p := counterLoop(0, 10, 1, 0, 12, inv, post)
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictInvalid {
		t.Fatalf("verdict = %s, want invalid", rep.Verdict)
	}
	if rep.Sufficiency.Holds {
		t.Fatal("sufficiency should not hold")
	}
	ce := rep.Sufficiency.Counterexample
	if ce == nil || (*ce)["x"] != 11 {
		t.Fatalf("sufficiency counterexample = %+v, want x=11", ce)
	}
}

func TestOutOfBoundsNotAProof(t *testing.T) {
	// Bounds [0,10], invariant x >= 0, guard x >= 0, body x := x + 1.
	// At x == 10 the successor 11 leaves the domain: the step must be
	// reported separately and the verdict must not be "valid".
	p := &Program{
		Variables: []string{"x"},
		Bounds:    map[string][2]int64{"x": {0, 10}},
		Init:      []Constraint{con(0, map[string]int64{"x": 1}, "==")},
		Guard:     []Constraint{con(0, map[string]int64{"x": 1}, ">=")},
		Body:      []Assignment{{Var: "x", Expr: expr(1, map[string]int64{"x": 1})}},
		Invariant: []Constraint{con(0, map[string]int64{"x": 1}, ">=")},
		Post:      []Constraint{con(0, nil, ">=")},
	}
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %s, want unknown", rep.Verdict)
	}
	if !rep.Preservation.Holds {
		t.Fatal("preservation should hold on in-bounds steps")
	}
	if len(rep.Preservation.OutOfBounds) != 1 {
		t.Fatalf("out-of-bounds = %+v, want exactly one", rep.Preservation.OutOfBounds)
	}
	tr := rep.Preservation.OutOfBounds[0]
	if tr.From["x"] != 10 || tr.To["x"] != "11" {
		t.Fatalf("out-of-bounds transition = %+v, want 10 -> 11", tr)
	}
}

func TestParallelAssignment(t *testing.T) {
	// Swap: x, y := y, x. Invariant x + y == 3 must be preserved.
	p := &Program{
		Variables: []string{"x", "y"},
		Bounds:    map[string][2]int64{"x": {0, 3}, "y": {0, 3}},
		Init: []Constraint{
			con(-1, map[string]int64{"x": 1}, "=="),
			con(-2, map[string]int64{"y": 1}, "=="),
		},
		Guard: []Constraint{con(0, map[string]int64{"x": 1}, ">")},
		Body: []Assignment{
			{Var: "x", Expr: expr(0, map[string]int64{"y": 1})},
			{Var: "y", Expr: expr(0, map[string]int64{"x": 1})},
		},
		Invariant: []Constraint{con(-3, map[string]int64{"x": 1, "y": 1}, "==")},
		Post:      []Constraint{con(-3, map[string]int64{"x": 1, "y": 1}, "==")},
	}
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictValid {
		t.Fatalf("verdict = %s, want valid: %+v", rep.Verdict, rep)
	}
}

func TestAffineExpressionBigValues(t *testing.T) {
	// Coefficients and values that overflow int64 arithmetic must still be
	// evaluated exactly (1<<62 * 1<<62).
	p := &Program{
		Variables: []string{"x"},
		Bounds:    map[string][2]int64{"x": {1 << 62, 1<<62 + 2}},
		Init:      []Constraint{con(-(1 << 62), map[string]int64{"x": 1}, "==")},
		Guard:     []Constraint{con(0, nil, "<")}, // never enters the loop
		Invariant: []Constraint{con(0, map[string]int64{"x": 1 << 62}, ">=")},
		Post:      []Constraint{con(0, map[string]int64{"x": 1 << 62}, ">=")},
	}
	mustValidate(t, p)
	rep := p.Check()
	if rep.Verdict != VerdictValid {
		t.Fatalf("verdict = %s, want valid: %+v", rep.Verdict, rep)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		prog *Program
	}{
		{"no variables", &Program{}},
		{"missing bounds", &Program{Variables: []string{"x"}}},
		{"empty bounds", &Program{Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {5, 1}}}},
		{"bad op", &Program{
			Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {0, 1}},
			Invariant: []Constraint{con(0, nil, "=>")},
		}},
		{"unknown var in guard", &Program{
			Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {0, 1}},
			Guard: []Constraint{con(0, map[string]int64{"z": 1}, ">=")},
		}},
		{"unknown assign target", &Program{
			Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {0, 1}},
			Body: []Assignment{{Var: "z", Expr: expr(0, nil)}},
		}},
		{"double assignment", &Program{
			Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {0, 1}},
			Body: []Assignment{
				{Var: "x", Expr: expr(0, nil)},
				{Var: "x", Expr: expr(1, nil)},
			},
		}},
		{"state space too large", &Program{
			Variables: []string{"x"}, Bounds: map[string][2]int64{"x": {0, 1 << 40}},
		}},
	}
	for _, tc := range cases {
		if err := tc.prog.Validate(1000000); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}
