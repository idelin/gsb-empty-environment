package msglang_test

import (
	"testing"

	"example.org/netsec/q0470/msglang"
)

func TestNodeValidationRejectsNilAndCycle(t *testing.T) {
	if err := ((*msglang.Node)(nil)).Validate(); err == nil {
		t.Fatal("nil node must fail validation")
	}

	leaf := msglang.NewLeaf("n", msglang.Nonce, []byte{1})
	cycle := msglang.NewNode("wrap", leaf)
	cycle.Children = append(cycle.Children, cycle)
	if err := cycle.Validate(); err == nil {
		t.Fatal("cyclic node graph must fail validation")
	}

	emptyLabel := &msglang.Node{Label: "", Leaf: &msglang.Leaf{Kind: msglang.Nonce}}
	if err := emptyLabel.Validate(); err == nil {
		t.Fatal("empty label must fail validation")
	}
}

func TestNewLeafDefensivelyCopiesValue(t *testing.T) {
	original := []byte{1, 2, 3}
	leaf := msglang.NewLeaf("n", msglang.Nonce, original)
	original[0] = 9
	if leaf.Leaf.Value[0] != 1 {
		t.Fatal("NewLeaf must copy the value slice")
	}
}

func TestBoundedIntersectWithoutNodeBudgetStillBoundedBySize(t *testing.T) {
	result, err := msglang.BoundedIntersect(
		msglang.AmbiguousAcknowledge(),
		msglang.AmbiguousAuthorization(),
		msglang.Bounds{MaxNodes: 6},
	)
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if result.Verdict != msglang.VerdictNonEmpty || result.NodeCount != 6 {
		t.Fatalf("expected the 6-node minimum without an explicit node budget, got %s", result.Verdict)
	}
}

func TestTwoParsingPathsBothValidateAgainstPhases(t *testing.T) {
	result, err := msglang.MinimalIntersection(
		msglang.AmbiguousAcknowledge(),
		msglang.AmbiguousAuthorization(),
	)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	okAck, derivAck, err := ack.Accept(result.Tree)
	if err != nil || !okAck {
		t.Fatalf("ack must accept witness: ok=%v err=%v", okAck, err)
	}
	okGrant, derivGrant, err := grant.Accept(result.Tree)
	if err != nil || !okGrant {
		t.Fatalf("grant must accept witness: ok=%v err=%v", okGrant, err)
	}
	if derivAck.String() != result.LeftTrace.String() {
		t.Fatal("left trace must match a direct ack derivation")
	}
	if derivGrant.String() != result.RightTrace.String() {
		t.Fatal("right trace must match a direct grant derivation")
	}
}

func TestNullaryInternalSymbolEnumeration(t *testing.T) {
	a := listAutomaton("a", "subject", msglang.Principal)
	b := listAutomaton("b", "subject", msglang.Principal)
	result, err := msglang.BoundedIntersect(a, b, msglang.Bounds{
		MaxNodes:   3,
		NodeBudget: 100000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if result.Verdict != msglang.VerdictNonEmpty || result.Tree.Label != "nil" {
		t.Fatalf("expected nullary nil witness from enumeration, got %s", result.Verdict)
	}
}
