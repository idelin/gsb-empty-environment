package msglang_test

import (
	"strings"
	"testing"

	"example.org/netsec/q0470/msglang"
)

func TestAmbiguousHandshakeIntersection(t *testing.T) {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	result, err := msglang.MinimalIntersection(ack, grant)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if result.Empty {
		t.Fatal("expected non-empty intersection for the ambiguous design")
	}
	if result.NodeCount != 6 {
		t.Fatalf("expected minimum of 6 nodes, got %d (%s)", result.NodeCount, result.Tree)
	}
	if result.Height != 2 {
		t.Fatalf("expected minimum height 3, got %d", result.Height)
	}

	if ok, _, err := ack.Accept(result.Tree); err != nil || !ok {
		t.Fatalf("ack phase does not accept witness: ok=%v err=%v", ok, err)
	}
	if ok, _, err := grant.Accept(result.Tree); err != nil || !ok {
		t.Fatalf("authorization phase does not accept witness: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(result.LeftTrace.Final, "ack:") ||
		!strings.HasPrefix(result.RightTrace.Final, "grant:") {
		t.Fatalf("derivations do not reach distinct phase finals: %q vs %q",
			result.LeftTrace.Final, result.RightTrace.Final)
	}
	if len(result.LeftTrace.Steps) != result.NodeCount ||
		len(result.RightTrace.Steps) != result.NodeCount {
		t.Fatalf("each parsing path must contain one reduction per node")
	}
	if result.LeftTrace.String() == result.RightTrace.String() {
		t.Fatal("the two parsing paths must be distinguishable")
	}

	tree := result.Tree.String()
	for _, label := range []string{"frame", "body", "subject", "nonce", "payload", "context"} {
		if !strings.Contains(tree, label) {
			t.Fatalf("witness missing label %q: %s", label, tree)
		}
	}
}

func TestSafeDesignIntersectionIsEmpty(t *testing.T) {
	result, err := msglang.MinimalIntersection(
		msglang.SafeAcknowledge(),
		msglang.SafeAuthorization(),
	)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if !result.Empty || result.Tree != nil {
		t.Fatalf("phase-specific body labels must make the intersection empty")
	}

	bounded, err := msglang.BoundedIntersect(
		msglang.SafeAcknowledge(),
		msglang.SafeAuthorization(),
		msglang.Bounds{MaxNodes: 12, MaxHeight: 4, NodeBudget: 2_000_000},
	)
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if bounded.Verdict != msglang.VerdictEmpty {
		t.Fatalf("exhausted safe universe must be Empty, got %s (%s)", bounded.Verdict, bounded.Reason)
	}
	if !bounded.ExactEmpty {
		t.Fatal("ExactEmpty should corroborate the bounded emptiness verdict")
	}
}

func TestMinimalWitnessIsSmallerThanLargerAcceptedTree(t *testing.T) {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()
	result, err := msglang.MinimalIntersection(ack, grant)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}

	large := msglang.NewNode(msglang.LabelFrame,
		msglang.NewNode(msglang.LabelBody,
			msglang.NewLeaf(msglang.LabelSubject, msglang.Principal, []byte("alice")),
			msglang.NewLeaf(msglang.LabelNonce, msglang.Nonce, []byte{1, 2, 3}),
			msglang.NewLeaf(msglang.LabelPayload, msglang.ByteString, []byte("grant")),
		),
		msglang.NewLeaf(msglang.LabelContext, msglang.ByteString, []byte("ctx")),
	)
	if ok, _, err := grant.Accept(large); err != nil || !ok {
		t.Fatalf("larger tree should be accepted: ok=%v err=%v", ok, err)
	}
	if result.NodeCount != large.NodeCount() {
		t.Fatalf("minimum shape should have the same node count %d, got %d",
			large.NodeCount(), result.NodeCount)
	}
	if result.Tree.String() == large.String() {
		t.Fatal("minimum witness should use canonical leaf values distinct from the concrete large tree")
	}
}

func TestBoundedFindsAmbiguousWitnessAndReturnsUnknownOnBudget(t *testing.T) {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	found, err := msglang.BoundedIntersect(ack, grant, msglang.Bounds{
		MaxNodes:   20,
		MaxHeight:  10,
		NodeBudget: 200000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if found.Verdict != msglang.VerdictNonEmpty || found.Tree == nil {
		t.Fatalf("expected NonEmpty, got %s: %s", found.Verdict, found.Reason)
	}
	if found.NodeCount != 6 {
		t.Fatalf("bounded search must find the 6-node minimum, got %d", found.NodeCount)
	}

	unknown, err := msglang.BoundedIntersect(ack, grant, msglang.Bounds{
		MaxNodes:   30,
		NodeBudget: 2,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if unknown.Verdict != msglang.VerdictUnknown {
		t.Fatalf("exhausted node budget must return Unknown, got %s", unknown.Verdict)
	}
	if unknown.ExactEmpty {
		t.Fatal("Unknown must not be confused with exact emptiness")
	}
	if unknown.ExactMinNodes != 6 {
		t.Fatalf("exact diagnostic should retain the true minimum 6, got %d", unknown.ExactMinNodes)
	}
	if unknown.NodesExamined == 0 || unknown.Reason == "" {
		t.Fatal("Unknown must report examined-node accounting and a reason")
	}
}

func TestSizeAndHeightBoundsReturnUnknownInsteadOfEmpty(t *testing.T) {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	small, err := msglang.BoundedIntersect(ack, grant, msglang.Bounds{
		MaxNodes:  5,
		MaxHeight: 10,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if small.Verdict != msglang.VerdictUnknown || small.Tree != nil {
		t.Fatalf("bound below minimum must be Unknown, got %s", small.Verdict)
	}

	shallow, err := msglang.BoundedIntersect(ack, grant, msglang.Bounds{
		MaxNodes:  12,
		MaxHeight: 1,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if shallow.Verdict != msglang.VerdictUnknown || shallow.Tree != nil {
		t.Fatalf("height below minimum must be Unknown, got %s", shallow.Verdict)
	}
}

func TestLeafKindAndExactValueMismatch(t *testing.T) {
	a := &msglang.Automaton{
		Name:   "exact",
		States: []string{"s", "f"},
		Final:  []string{"f"},
		Leaves: []msglang.LeafRule{
			{Label: "p", Kind: msglang.Principal, Exact: true, Want: []byte("alice"), State: "s"},
		},
		Rules: []msglang.Rule{
			{Label: "wrap", Child: []string{"s"}, State: "f"},
		},
	}

	good := msglang.NewNode("wrap", msglang.NewLeaf("p", msglang.Principal, []byte("alice")))
	badValue := msglang.NewNode("wrap", msglang.NewLeaf("p", msglang.Principal, []byte("bob")))
	badKind := msglang.NewNode("wrap", msglang.NewLeaf("p", msglang.Nonce, []byte("alice")))

	if ok, _, err := a.Accept(good); err != nil || !ok {
		t.Fatalf("exact alice should be accepted: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := a.Accept(badValue); ok {
		t.Fatal("bob must not satisfy the exact alice leaf rule")
	}
	if ok, _, _ := a.Accept(badKind); ok {
		t.Fatal("a nonce must not be accepted where a principal is required")
	}

	minimum, err := msglang.MinimalTree(a)
	if err != nil {
		t.Fatalf("MinimalTree: %v", err)
	}
	if minimum.Empty || minimum.NodeCount != 2 {
		t.Fatalf("expected 2-node exact minimum, got empty=%v count=%d", minimum.Empty, minimum.NodeCount)
	}
	if string(minimum.Tree.Children[0].Leaf.Value) != "alice" {
		t.Fatalf("minimum must preserve exact leaf value, got %v", minimum.Tree.Children[0].Leaf.Value)
	}
}
