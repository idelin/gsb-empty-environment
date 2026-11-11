package netsec

import "testing"

func TestFormulaRenderingAndEval(t *testing.T) {
	x0, x1 := Var{V: 0}, Var{V: 1}
	f := Or{Fs: []Formula{
		And{Fs: []Formula{x0, Not{F: x1}}},
		Not{F: x0},
		Const{V: true},
		Const{V: false},
	}}
	want := "((x0 & !x1) | !x0 | true | false)"
	if got := f.String(); got != want {
		t.Fatalf("rendering mismatch:\n got %s\nwant %s", got, want)
	}
	env := map[int]bool{0: true, 1: false}
	if !f.Eval(env) {
		t.Fatal("formula must evaluate true at x0=true,x1=false")
	}
	if got := Vars(f); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("unexpected variables: %v", got)
	}
	if (Or{}).Eval(env) {
		t.Fatal("empty disjunction must be false")
	}
	if !(And{}).Eval(env) {
		t.Fatal("empty conjunction must be true")
	}
}

func TestVerifyWitnessRejectsBadWitness(t *testing.T) {
	x := MakeLit(0)
	cnf := CNF{{x}}
	if VerifyWitness(cnf, map[int]bool{0: false}) {
		t.Fatal("witness falsifying a clause must be rejected")
	}
	if VerifyWitness(cnf, nil) {
		t.Fatal("nil witness must be rejected")
	}
	if VerifyWitness(cnf, map[int]bool{}) {
		t.Fatal("partial witness must be rejected")
	}
}

func TestVerifyInterpolantDetectsTampering(t *testing.T) {
	x := MakeLit(0)
	a := CNF{{x}}
	b := CNF{{x.Neg()}}

	// Correct interpolant passes.
	good := Var{V: 0}
	report, err := VerifyInterpolant(a, b, []int{0}, good)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("valid interpolant rejected: %s", report.Failure)
	}

	// Its negation violates both properties and must be caught.
	bad := Not{F: good}
	report, err = VerifyInterpolant(a, b, []int{0}, bad)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.ImplicationOK || report.ContradictionOK {
		t.Fatalf("tampered interpolant not detected: %+v", report)
	}

	// A formula leaking a non-shared variable must fail the language check.
	leak := And{Fs: []Formula{good, Var{V: 7}}}
	report, err = VerifyInterpolant(a, b, []int{0}, leak)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.LanguageOK {
		t.Fatalf("non-shared variable not detected: %+v", report)
	}
}

func TestVerifyInterpolantRejectsSatInputAndNil(t *testing.T) {
	x := MakeLit(0)
	a := CNF{{x}}
	b := CNF{{x}} // joint is SAT
	if _, err := VerifyInterpolant(a, b, []int{0}, Var{V: 0}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	report, err := VerifyInterpolant(a, b, []int{0}, Var{V: 0})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("SAT joint input must not admit a passing interpolant")
	}
	if _, err := VerifyInterpolant(a, b, []int{0}, nil); err == nil {
		t.Fatal("nil interpolant must return an error")
	}
}

func TestInputErrorMessage(t *testing.T) {
	e := &InputError{Op: "cnf", Clause: 3, Msg: "bad"}
	if e.Error() == "" {
		t.Fatal("error message must be non-empty")
	}
	e2 := &InputError{Op: "shared", Msg: "bad"}
	if e2.Error() == "" {
		t.Fatal("error message must be non-empty")
	}
}

func TestLiteralAndClauseEvalPartial(t *testing.T) {
	x := MakeLit(2)
	if _, ok := x.Eval(map[int]bool{}); ok {
		t.Fatal("unassigned literal must report not-ok")
	}
	clause := Clause{x, MakeLit(3).Neg()}
	if _, ok := clause.Eval(map[int]bool{2: true}); !ok {
		t.Fatal("satisfied clause should be decidable even if another var is free")
	}
	cnf := CNF{clause}
	if _, ok := cnf.Eval(map[int]bool{}); ok {
		t.Fatal("undecidable CNF must report not-ok")
	}
}
