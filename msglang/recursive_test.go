package msglang_test

import (
	"testing"

	"example.org/netsec/q0470/msglang"
)

// listAutomaton recognizes nil() and cons(item, list) over typed items.
func listAutomaton(name, itemLabel string, itemKind msglang.LeafKind) *msglang.Automaton {
	return &msglang.Automaton{
		Name:   name,
		States: []string{"item", "list", "nonempty"},
		Final:  []string{"list"},
		Leaves: []msglang.LeafRule{
			{Label: itemLabel, Kind: itemKind, State: "item"},
		},
		Rules: []msglang.Rule{
			{Label: "nil", State: "list"},
			{Label: "cons", Child: []string{"item", "list"}, State: "list"},
		},
	}
}

func TestRecursiveTypeExactAndBoundedDepth(t *testing.T) {
	left := listAutomaton("left-list", "subject", msglang.Principal)
	right := listAutomaton("right-list", "subject", msglang.Principal)

	minimum, err := msglang.MinimalIntersection(left, right)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if minimum.Empty || minimum.NodeCount != 1 || minimum.Tree.Label != "nil" {
		t.Fatalf("recursive minimum should be nil(), got empty=%v tree=%v", minimum.Empty, minimum.Tree)
	}

	// Enumeration must terminate on the finite-height universe even though the
	// recursive grammar has infinitely many accepted trees.
	bounded, err := msglang.BoundedIntersect(left, right, msglang.Bounds{
		MaxNodes:   5,
		MaxHeight:  2,
		NodeBudget: 100000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if bounded.Verdict != msglang.VerdictNonEmpty {
		t.Fatalf("nil() should be found by enumeration, got %s: %s", bounded.Verdict, bounded.Reason)
	}
	if bounded.Tree.Label != "nil" || bounded.NodeCount != 1 {
		t.Fatalf("enumeration minimum should still be nil(), got %s", bounded.Tree)
	}

	// A non-empty list needs at least 3 nodes: cons(item, nil).
	nonEmpty := msglang.NewNode("cons",
		msglang.NewLeaf("subject", msglang.Principal, []byte("alice")),
		msglang.NewNode("nil"),
	)
	if ok, derivation, err := left.Accept(nonEmpty); err != nil || !ok {
		t.Fatalf("non-empty recursive list should be accepted: ok=%v err=%v", ok, err)
	} else if len(derivation.Steps) != 3 {
		t.Fatalf("expected three postorder steps, got %d", len(derivation.Steps))
	}
}

func TestRecursiveMinimumAboveHeightBoundIsUnknown(t *testing.T) {
	// This automaton has no nullary list, so the shallowest accepted tree is
	// the finite-height cons chain reaching a terminal leaf state.
	deep := &msglang.Automaton{
		Name:   "deep",
		States: []string{"item", "list", "final"},
		Final:  []string{"final"},
		Leaves: []msglang.LeafRule{
			{Label: "item", Kind: msglang.ByteString, State: "item"},
		},
		Rules: []msglang.Rule{
			{Label: "cons", Child: []string{"item", "item"}, State: "list"},
			{Label: "wrap", Child: []string{"list"}, State: "final"},
		},
	}
	minimum, err := msglang.MinimalTree(deep)
	if err != nil {
		t.Fatalf("MinimalTree: %v", err)
	}
	if minimum.Empty || minimum.Height != 2 {
		t.Fatalf("expected height-2 minimum, got empty=%v height=%d", minimum.Empty, minimum.Height)
	}

	bounded, err := msglang.BoundedIntersect(deep, deep, msglang.Bounds{
		MaxNodes:   20,
		MaxHeight:  1,
		NodeBudget: 100000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if bounded.Verdict != msglang.VerdictUnknown {
		t.Fatalf("height-1 universe cannot contain a height-2 witness; expected Unknown, got %s (%s)",
			bounded.Verdict, bounded.Reason)
	}
}

func TestEmptyFieldLeafIsExplicitlyAccepted(t *testing.T) {
	emptyOnly := &msglang.Automaton{
		Name:   "empty-field",
		States: []string{"s"},
		Final:  []string{"s"},
		Leaves: []msglang.LeafRule{
			{Label: "payload", Kind: msglang.ByteString, Exact: true, Want: []byte{}, State: "s"},
		},
	}
	wildcard := &msglang.Automaton{
		Name:   "wildcard",
		States: []string{"s"},
		Final:  []string{"s"},
		Leaves: []msglang.LeafRule{
			{Label: "payload", Kind: msglang.ByteString, State: "s"},
		},
	}

	result, err := msglang.MinimalIntersection(emptyOnly, wildcard)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if result.Empty || len(result.Tree.Leaf.Value) != 0 {
		t.Fatalf("expected empty-byte witness, got empty=%v tree=%s", result.Empty, result.Tree)
	}

	bounded, err := msglang.BoundedIntersect(emptyOnly, wildcard, msglang.Bounds{
		MaxNodes:   5,
		NodeBudget: 100000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if bounded.Verdict != msglang.VerdictNonEmpty ||
		len(bounded.Tree.Leaf.Value) != 0 {
		t.Fatalf("finite enumeration must include the empty field, got %s tree=%s",
			bounded.Verdict, bounded.Tree)
	}

	nonEmpty := msglang.NewLeaf("payload", msglang.ByteString, []byte{1})
	if ok, _, _ := emptyOnly.Accept(nonEmpty); ok {
		t.Fatal("non-empty bytes must not satisfy an exact empty-field rule")
	}
}

func TestEmptyLanguageIsDetected(t *testing.T) {
	// No rule can produce the final state.
	dead := &msglang.Automaton{
		Name:   "dead",
		States: []string{"seed", "unreachable"},
		Final:  []string{"unreachable"},
		Leaves: []msglang.LeafRule{
			{Label: "n", Kind: msglang.Nonce, State: "seed"},
		},
	}
	result, err := msglang.MinimalTree(dead)
	if err != nil {
		t.Fatalf("MinimalTree: %v", err)
	}
	if !result.Empty {
		t.Fatal("automaton with an unreachable final state must be empty")
	}

	bounded, err := msglang.BoundedIntersect(dead, dead, msglang.Bounds{
		MaxNodes:   4,
		NodeBudget: 100000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	if bounded.Verdict != msglang.VerdictEmpty || !bounded.ExactEmpty {
		t.Fatalf("empty language must remain Empty after bounded enumeration, got %s", bounded.Verdict)
	}
}

func TestConflictingExactLeavesMakeIntersectionEmpty(t *testing.T) {
	left := &msglang.Automaton{
		Name:   "left",
		States: []string{"s"},
		Final:  []string{"s"},
		Leaves: []msglang.LeafRule{
			{Label: "p", Kind: msglang.Principal, Exact: true, Want: []byte("alice"), State: "s"},
		},
	}
	right := &msglang.Automaton{
		Name:   "right",
		States: []string{"s"},
		Final:  []string{"s"},
		Leaves: []msglang.LeafRule{
			{Label: "p", Kind: msglang.Principal, Exact: true, Want: []byte("bob"), State: "s"},
		},
	}
	result, err := msglang.MinimalIntersection(left, right)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if !result.Empty {
		t.Fatal("incompatible exact leaf values must make the intersection empty")
	}
}

func TestExactAndWildcardLeafNarrowToExact(t *testing.T) {
	exact := &msglang.Automaton{
		Name:   "exact-side",
		States: []string{"s"},
		Final:  []string{"s"},
		Leaves: []msglang.LeafRule{
			{Label: "p", Kind: msglang.Principal, Exact: true, Want: []byte("alice"), State: "s"},
		},
	}
	wildcard := &msglang.Automaton{
		Name:   "wildcard-side",
		States: []string{"t"},
		Final:  []string{"t"},
		Leaves: []msglang.LeafRule{
			{Label: "p", Kind: msglang.Principal, State: "t"},
		},
	}
	result, err := msglang.MinimalIntersection(exact, wildcard)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if result.Empty {
		t.Fatal("exact intersect wildcard should be non-empty")
	}
	if string(result.Tree.Leaf.Value) != "alice" {
		t.Fatalf("intersection must narrow to the exact value alice, got %q",
			result.Tree.Leaf.Value)
	}
	if len(result.Product.Leaves) != 1 || !result.Product.Leaves[0].Exact {
		t.Fatal("product leaf rule must retain the exact-value constraint")
	}

	otherValue := msglang.NewLeaf("p", msglang.Principal, []byte("bob"))
	if ok, _, _ := result.Product.Accept(otherValue); ok {
		t.Fatal("product automaton must reject a value outside the exact leaf set")
	}
}

func TestValidationRejectsMalformedAutomataAndBounds(t *testing.T) {
	cases := map[string]*msglang.Automaton{
		"no states": {
			Name:   "x",
			Final:  []string{"f"},
			Leaves: []msglang.LeafRule{{Label: "n", Kind: msglang.Nonce, State: "f"}},
		},
		"unknown final": {
			Name:   "x",
			States: []string{"s"},
			Final:  []string{"f"},
			Leaves: []msglang.LeafRule{{Label: "n", Kind: msglang.Nonce, State: "s"}},
		},
		"duplicate leaf label": {
			Name:   "x",
			States: []string{"s"},
			Final:  []string{"s"},
			Leaves: []msglang.LeafRule{
				{Label: "n", Kind: msglang.Nonce, State: "s"},
				{Label: "n", Kind: msglang.Nonce, State: "s"},
			},
		},
		"leaf and internal share label": {
			Name:   "x",
			States: []string{"s", "f"},
			Final:  []string{"f"},
			Leaves: []msglang.LeafRule{{Label: "n", Kind: msglang.Nonce, State: "s"}},
			Rules:  []msglang.Rule{{Label: "n", Child: []string{"s"}, State: "f"}},
		},
	}
	for name, automaton := range cases {
		t.Run(name, func(t *testing.T) {
			if err := automaton.Validate(); err == nil {
				t.Fatalf("%s should fail validation", name)
			}
		})
	}

	for _, bounds := range []msglang.Bounds{
		{MaxNodes: 0},
		{MaxNodes: -1},
		{MaxNodes: 2, NodeBudget: -1},
	} {
		if err := bounds.Validate(); err == nil {
			t.Fatalf("bounds %+v should fail validation", bounds)
		}
	}
}
