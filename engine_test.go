package reasoner

import (
	"reflect"
	"strings"
	"testing"
)

// Acceptance: A and A->B derive B.
func TestBasicForwardChaining(t *testing.T) {
	e := New(&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"})
	if e.Known("B") {
		t.Fatal("B should not be known before asserting A")
	}
	e.Assert("A")
	if !e.Known("B") {
		t.Fatal("B should be derived from A and A->B")
	}
}

// Acceptance: with A->B and C->B, retracting A keeps B (via C);
// retracting C afterwards removes B entirely.
func TestIndependentSupportAndCascade(t *testing.T) {
	e := New(
		&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"},
		&Rule{Name: "r2", Premises: []Fact{"C"}, Conclusion: "B"},
	)
	e.Assert("A")
	e.Assert("C")
	if !e.Known("B") {
		t.Fatal("B should be derived")
	}

	if err := e.Retract("A"); err != nil {
		t.Fatalf("retract A: %v", err)
	}
	if !e.Known("B") {
		t.Fatal("B must survive retraction of A because C->B still supports it")
	}

	if err := e.Retract("C"); err != nil {
		t.Fatalf("retract C: %v", err)
	}
	if e.Known("B") {
		t.Fatal("B must be cleared after losing all support")
	}
	if got := e.Facts(); len(got) != 0 {
		t.Fatalf("expected no facts left, got %v", got)
	}
}

// Multi-hop cascade: A->B->C collapses entirely when A is retracted.
func TestMultiHopCascade(t *testing.T) {
	e := New(
		&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"},
		&Rule{Name: "r2", Premises: []Fact{"B"}, Conclusion: "C"},
	)
	e.Assert("A")
	if !e.Known("C") {
		t.Fatal("C should be derived transitively")
	}
	if err := e.Retract("A"); err != nil {
		t.Fatalf("retract A: %v", err)
	}
	if e.Known("B") || e.Known("C") {
		t.Fatalf("B and C should be cleared, facts left: %v", e.Facts())
	}
}

// A fact supported by an independent derivation survives retraction of the
// asserted form, and vice versa.
func TestAssertedAndDerivedCoexist(t *testing.T) {
	e := New(&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"})
	e.Assert("A")
	e.Assert("B") // B is both asserted and derived
	if err := e.Retract("B"); err != nil {
		t.Fatalf("retract B: %v", err)
	}
	if !e.Known("B") {
		t.Fatal("B should remain known via derivation r1")
	}
	if err := e.Retract("A"); err != nil {
		t.Fatalf("retract A: %v", err)
	}
	if e.Known("B") {
		t.Fatal("B should be gone once its derivation is also gone")
	}
}

// Retracting a fact that was never asserted is an error.
func TestRetractNonAsserted(t *testing.T) {
	e := New(&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"})
	e.Assert("A")
	if err := e.Retract("B"); err == nil {
		t.Fatal("retracting a merely derived fact should fail")
	}
	if err := e.Retract("Z"); err == nil {
		t.Fatal("retracting an unknown fact should fail")
	}
}

// Multi-premise Horn clause: conclusion only fires when all premises hold.
func TestMultiPremiseRule(t *testing.T) {
	e := New(&Rule{Name: "r1", Premises: []Fact{"A", "B"}, Conclusion: "C"})
	e.Assert("A")
	if e.Known("C") {
		t.Fatal("C must wait for all premises")
	}
	e.Assert("B")
	if !e.Known("C") {
		t.Fatal("C should fire once A and B hold")
	}
	if err := e.Retract("B"); err != nil {
		t.Fatalf("retract B: %v", err)
	}
	if e.Known("C") {
		t.Fatal("C should be cleared when one premise is lost")
	}
}

// Minimal explanation: the shortest evidence chain is chosen.
func TestMinimalExplanation(t *testing.T) {
	e := New(
		&Rule{Name: "short", Premises: []Fact{"A"}, Conclusion: "D"},
		&Rule{Name: "long1", Premises: []Fact{"A"}, Conclusion: "B"},
		&Rule{Name: "long2", Premises: []Fact{"B"}, Conclusion: "C"},
		&Rule{Name: "long3", Premises: []Fact{"C"}, Conclusion: "D"},
	)
	e.Assert("A")
	proof, err := e.Explain("D")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if proof.Rule == nil || proof.Rule.Name != "short" {
		t.Fatalf("expected the short derivation, got:\n%s", proof)
	}
	if proof.Depth() != 1 {
		t.Fatalf("expected depth 1, got %d", proof.Depth())
	}
	if got, want := proof.BaseFacts(), []Fact{"A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("base facts = %v, want %v", got, want)
	}
	steps := proof.Steps()
	if len(steps) != 2 || !strings.Contains(steps[1], "short") {
		t.Fatalf("unexpected steps: %v", steps)
	}
}

// Explanation of a base fact is trivial; unknown facts cannot be explained.
func TestExplainBaseAndUnknown(t *testing.T) {
	e := New()
	e.Assert("A")
	proof, err := e.Explain("A")
	if err != nil {
		t.Fatalf("explain base: %v", err)
	}
	if !proof.Asserted() || proof.Depth() != 0 {
		t.Fatalf("base proof malformed: %s", proof)
	}
	if _, err := e.Explain("Z"); err == nil {
		t.Fatal("explaining an unknown fact should fail")
	}
}

// Cyclic rules must not hang the explainer; asserted facts still explain.
func TestCyclicRulesTerminate(t *testing.T) {
	e := New(
		&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"},
		&Rule{Name: "r2", Premises: []Fact{"B"}, Conclusion: "A"},
	)
	e.Assert("A")
	if !e.Known("B") {
		t.Fatal("B should be derived")
	}
	proof, err := e.Explain("B")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if got, want := proof.BaseFacts(), []Fact{"A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("base facts = %v, want %v", got, want)
	}
}

// Re-asserting after retraction re-derives the closure.
func TestReAssertAfterRetraction(t *testing.T) {
	e := New(&Rule{Name: "r1", Premises: []Fact{"A"}, Conclusion: "B"})
	e.Assert("A")
	if err := e.Retract("A"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if e.Known("B") {
		t.Fatal("B should be cleared")
	}
	e.Assert("A")
	if !e.Known("B") {
		t.Fatal("B should be re-derived after re-asserting A")
	}
}
