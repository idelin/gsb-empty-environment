package netsec

import "testing"

func TestSolveWitnessSatisfiesInput(t *testing.T) {
	x, y := MakeLit(0), MakeLit(1)
	cnf := CNF{
		{x, y},
		{x.Neg(), y},
		{x.Neg(), y.Neg()},
	}
	ok, witness := Solve(cnf)
	if !ok {
		t.Fatal("expected SAT")
	}
	if !VerifyWitness(cnf, witness) {
		t.Fatalf("returned witness %v does not satisfy CNF", witness)
	}
}

func TestSolveUnsat(t *testing.T) {
	x := MakeLit(0)
	cnf := CNF{{x}, {x.Neg()}}
	if ok, witness := Solve(cnf); ok || witness != nil {
		t.Fatalf("expected UNSAT, got ok=%v witness=%v", ok, witness)
	}
}

func TestEmptyCnfIsTrue(t *testing.T) {
	ok, witness := Solve(CNF{})
	if !ok {
		t.Fatal("empty CNF must be satisfiable")
	}
	if !VerifyWitness(CNF{}, witness) {
		t.Fatal("witness check failed for vacuously true CNF")
	}
}

func TestEmptyClauseIsFalse(t *testing.T) {
	if Sat(CNF{{}, {MakeLit(0)}}) {
		t.Fatal("CNF containing an empty clause must be UNSAT")
	}
}

func TestUnitPropagationConflict(t *testing.T) {
	// Forced: x=true, y=false, then (¬x ∨ y) conflicts.
	x, y := MakeLit(0), MakeLit(1)
	cnf := CNF{
		{x},
		{y.Neg()},
		{x.Neg(), y},
	}
	if Sat(cnf) {
		t.Fatal("expected UNSAT through unit propagation")
	}
}

func TestLiteralEncoding(t *testing.T) {
	l := MakeLit(3)
	if l.Var() != 3 || !l.Sign() {
		t.Fatalf("positive literal encoding broken: %+v", l)
	}
	n := l.Neg()
	if n.Var() != 3 || n.Sign() {
		t.Fatalf("negation encoding broken: %+v", n)
	}
	if n.Neg() != l {
		t.Fatal("double negation must return the original literal")
	}
}

func TestInvalidCnf(t *testing.T) {
	bad := MakeLit(MaxVarIndex + 1)
	if _, err := Interpolate(CNF{{bad}}, CNF{}, nil); err == nil {
		t.Fatal("expected error for out-of-range variable")
	}
}
