package engine

import (
	"reflect"
	"testing"
)

func mustGet(t *testing.T, e *Engine, name string) float64 {
	t.Helper()
	v, err := e.Get(name)
	if err != nil {
		t.Fatalf("Get(%s): %v", name, err)
	}
	return v
}

// Acceptance: build A1->B1->C1 and D1->C1. When A1 changes, only B1 and
// C1 are recomputed; D1 stays untouched.
func TestIncrementalRecomputeOnlyAffectedSubgraph(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("D1", 10); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "A1*2"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "B1+D1"); err != nil {
		t.Fatal(err)
	}
	// An unrelated formula cell that does NOT depend on A1.
	if err := e.SetFormula("E1", "D1*3"); err != nil {
		t.Fatal(err)
	}

	if got := mustGet(t, e, "B1"); got != 2 {
		t.Fatalf("B1 = %v, want 2", got)
	}
	if got := mustGet(t, e, "C1"); got != 12 {
		t.Fatalf("C1 = %v, want 12", got)
	}
	if got := mustGet(t, e, "E1"); got != 30 {
		t.Fatalf("E1 = %v, want 30", got)
	}
	if n := e.EvalCount("B1"); n != 1 {
		t.Fatalf("B1 evaluated %d times, want 1", n)
	}
	if n := e.EvalCount("C1"); n != 1 {
		t.Fatalf("C1 evaluated %d times, want 1", n)
	}
	if n := e.EvalCount("E1"); n != 1 {
		t.Fatalf("E1 evaluated %d times, want 1", n)
	}

	// Single-point change at A1.
	if err := e.SetValue("A1", 5); err != nil {
		t.Fatal(err)
	}

	if got := mustGet(t, e, "B1"); got != 10 {
		t.Fatalf("B1 = %v, want 10", got)
	}
	if got := mustGet(t, e, "C1"); got != 20 {
		t.Fatalf("C1 = %v, want 20", got)
	}
	if got := mustGet(t, e, "D1"); got != 10 {
		t.Fatalf("D1 = %v, want 10 (unchanged)", got)
	}
	// Only B1 and C1 recomputed exactly once more.
	if n := e.EvalCount("B1"); n != 2 {
		t.Fatalf("B1 evaluated %d times, want 2", n)
	}
	if n := e.EvalCount("C1"); n != 2 {
		t.Fatalf("C1 evaluated %d times, want 2", n)
	}
	// E1 does not depend on A1: must not be recomputed.
	if n := e.EvalCount("E1"); n != 1 {
		t.Fatalf("E1 evaluated %d times, want 1 (unaffected subgraph)", n)
	}
}

// Acceptance: adding C1->A1 (A1 = C1+1) is rejected immediately with the
// full cycle path, and the engine state is isolated (unchanged).
func TestCycleRejectedWithFullPathAndIsolated(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "A1+1"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "B1+1"); err != nil {
		t.Fatal(err)
	}

	err := e.SetFormula("A1", "C1+1")
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T (%v)", err, err)
	}
	want := []string{"A1", "C1", "B1", "A1"}
	if !reflect.DeepEqual(ce.Path, want) {
		t.Fatalf("cycle path = %v, want %v", ce.Path, want)
	}
	t.Logf("cycle error message: %v", ce)

	// Isolation: A1 keeps its literal value, no formula was installed,
	// and the rest of the graph still evaluates correctly.
	if got := mustGet(t, e, "A1"); got != 1 {
		t.Fatalf("A1 = %v, want 1 (state must be untouched)", got)
	}
	if got := mustGet(t, e, "C1"); got != 3 {
		t.Fatalf("C1 = %v, want 3", got)
	}
	if err := e.SetValue("A1", 100); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 102 {
		t.Fatalf("C1 = %v, want 102 after A1=100", got)
	}
}

func TestSelfReferenceRejected(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 1); err != nil {
		t.Fatal(err)
	}
	err := e.SetFormula("A1", "A1+1")
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T (%v)", err, err)
	}
	want := []string{"A1", "A1"}
	if !reflect.DeepEqual(ce.Path, want) {
		t.Fatalf("cycle path = %v, want %v", ce.Path, want)
	}
}

func TestIndirectCycleViaDiamond(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "A1*2"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "A1*3"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("D1", "B1+C1"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "D1"); got != 10 {
		t.Fatalf("D1 = %v, want 10", got)
	}
	// A1 = D1+1 would close A1 -> D1 -> ... -> A1.
	err := e.SetFormula("A1", "D1+1")
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T (%v)", err, err)
	}
	if ce.Path[0] != "A1" || ce.Path[len(ce.Path)-1] != "A1" {
		t.Fatalf("cycle path %v must start and end at A1", ce.Path)
	}
	t.Logf("diamond cycle: %v", ce)
}

func TestDiamondRecomputesOncePerCell(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "A1+1"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "A1+2"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("D1", "B1+C1"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("A1", 10); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "D1"); got != 23 {
		t.Fatalf("D1 = %v, want 23", got)
	}
	// Diamond: D1 must be evaluated exactly once per change, not twice.
	if n := e.EvalCount("D1"); n != 2 {
		t.Fatalf("D1 evaluated %d times, want 2", n)
	}
}

func TestFormulaReplacementRewiresGraph(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("B1", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "A1+10"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 11 {
		t.Fatalf("C1 = %v, want 11", got)
	}
	// Rewire C1 to depend on B1 instead of A1.
	if err := e.SetFormula("C1", "B1*10"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 20 {
		t.Fatalf("C1 = %v, want 20", got)
	}
	base := e.EvalCount("C1")
	// Changing A1 must no longer affect C1.
	if err := e.SetValue("A1", 99); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 20 {
		t.Fatalf("C1 = %v, want 20 (A1 edge removed)", got)
	}
	if n := e.EvalCount("C1"); n != base {
		t.Fatalf("C1 recomputed after A1 change; evalCount %d, want %d", n, base)
	}
	// Changing B1 still propagates.
	if err := e.SetValue("B1", 5); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 50 {
		t.Fatalf("C1 = %v, want 50", got)
	}
}

func TestInvalidFormulaRejectedWithoutSideEffects(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 7); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"A1+", "(A1", "A1**2", "", "=", "A1 + Z9"} {
		if err := e.SetFormula("B1", f); err == nil {
			t.Fatalf("SetFormula(%q) should fail", f)
		}
	}
	if _, err := e.Get("B1"); err == nil {
		t.Fatal("B1 must not exist after rejected formulas")
	}
	if got := mustGet(t, e, "A1"); got != 7 {
		t.Fatalf("A1 = %v, want 7", got)
	}
}

func TestSetValueClearsFormula(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 3); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "A1*2"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("B1", 42); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("A1", 100); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "B1"); got != 42 {
		t.Fatalf("B1 = %v, want 42 (formula cleared)", got)
	}
}

func TestArithmeticAndParentheses(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 6); err != nil {
		t.Fatal(err)
	}
	if err := e.SetValue("B1", 3); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("C1", "=(A1+B1)*2-A1/B1"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "C1"); got != 16 {
		t.Fatalf("C1 = %v, want 16", got)
	}
	if err := e.SetFormula("D1", "-A1 + -B1"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, e, "D1"); got != -9 {
		t.Fatalf("D1 = %v, want -9", got)
	}
}

func TestDivisionByZeroReported(t *testing.T) {
	e := New()
	if err := e.SetValue("A1", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.SetFormula("B1", "10/A1"); err == nil {
		t.Fatal("expected division by zero error")
	}
}
