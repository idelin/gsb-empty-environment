package krb5policy

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func newTestGraph(t *testing.T, edges []TrustEdge, capaths []CapathRule, tune func(*TrustGraphConfig)) *TrustGraph {
	t.Helper()
	cfg := TrustGraphConfig{Edges: edges, Capaths: capaths}
	if tune != nil {
		tune(&cfg)
	}
	g, err := NewTrustGraph(cfg)
	if err != nil {
		t.Fatalf("NewTrustGraph: %v", err)
	}
	return g
}

func TestDirectedAndBidirectionalTrust(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"},
		{From: "B", To: "C", Bidirectional: true},
	}, nil, nil)
	if _, err := g.ValidatePath("A", "C", []string{"B"}); err != nil {
		t.Fatalf("A->B->C: %v", err)
	}
	if _, err := g.ValidatePath("C", "A", []string{"B"}); !errors.Is(err, ErrNoTrust) {
		t.Fatalf("reverse path should fail, got %v", err)
	}
	if _, err := g.ValidatePath("C", "B", nil); err != nil {
		t.Fatalf("bidirectional C->B: %v", err)
	}
	if g.HasDirectTrust("A", "C") {
		t.Fatal("unexpected A->C edge")
	}
	if g.HasDirectTrust("C", "B") == false {
		t.Fatal("missing bidirectional C->B edge")
	}
}

func TestCapathStrictSet(t *testing.T) {
	edges := []TrustEdge{
		{From: "CLIENT", To: "EDGE"},
		{From: "EDGE", To: "CORE"},
		{From: "EDGE", To: "TRUSTED"},
		{From: "TRUSTED", To: "EDGE"},
		{From: "TRUSTED", To: "CORE"},
		{From: "CLIENT", To: "TRUSTED"},
		{From: "CLIENT", To: "OTHER"},
		{From: "OTHER", To: "CORE"},
		{From: "CLIENT", To: "CORE"},
	}
	capaths := []CapathRule{{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"EDGE", "TRUSTED"}}}
	g := newTestGraph(t, edges, capaths, nil)

	if _, err := g.ValidatePath("CLIENT", "CORE", []string{"EDGE", "TRUSTED"}); err != nil {
		t.Fatalf("ordered capath: %v", err)
	}
	if _, err := g.ValidatePath("CLIENT", "CORE", []string{"TRUSTED", "EDGE"}); err != nil {
		t.Fatalf("strict set should allow order flexibility: %v", err)
	}
	if _, err := g.ValidatePath("CLIENT", "CORE", []string{"EDGE"}); !errors.Is(err, ErrCapathViolation) {
		t.Fatalf("missing configured hop: %v", err)
	}
	if _, err := g.ValidatePath("CLIENT", "CORE", []string{"OTHER"}); !errors.Is(err, ErrCapathViolation) {
		t.Fatalf("unlisted hop: %v", err)
	}
	if _, err := g.ValidatePath("CLIENT", "CORE", nil); !errors.Is(err, ErrTransitShortcut) {
		t.Fatalf("direct shortcut: %v", err)
	}
}

func TestCapathFirstHopRequirement(t *testing.T) {
	edges := []TrustEdge{
		{From: "C", To: "A"}, {From: "A", To: "B"}, {From: "B", To: "A"}, {From: "A", To: "S"}, {From: "C", To: "B"}, {From: "B", To: "S"},
	}
	capaths := []CapathRule{{ClientRealm: "C", ServerRealm: "S", Intermediates: []string{"A", "B"}}}
	g := newTestGraph(t, edges, capaths, func(cfg *TrustGraphConfig) {
		cfg.RequireFirstCapathHop = true
	})
	if _, err := g.ValidatePath("C", "S", []string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ValidatePath("C", "S", []string{"B", "A"}); !errors.Is(err, ErrCapathViolation) {
		t.Fatalf("first-hop rule: %v", err)
	}
}

func TestCapathSubsetAndExactPolicies(t *testing.T) {
	edges := []TrustEdge{
		{From: "CLIENT", To: "EDGE"},
		{From: "EDGE", To: "CORE"},
		{From: "EDGE", To: "TRUSTED"},
		{From: "CLIENT", To: "TRUSTED"},
		{From: "TRUSTED", To: "EDGE"},
		{From: "TRUSTED", To: "CORE"},
		{From: "CLIENT", To: "CORE"},
	}
	capaths := []CapathRule{{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"EDGE", "TRUSTED"}}}
	subset := newTestGraph(t, edges, capaths, func(cfg *TrustGraphConfig) {
		cfg.Policy = CapathAllowedSubset
	})
	if _, err := subset.ValidatePath("CLIENT", "CORE", []string{"EDGE"}); err != nil {
		t.Fatalf("subset should permit one configured hop: %v", err)
	}
	if _, err := subset.ValidatePath("CLIENT", "CORE", nil); !errors.Is(err, ErrTransitShortcut) {
		t.Fatalf("subset direct shortcut: %v", err)
	}

	exact := newTestGraph(t, edges, capaths, func(cfg *TrustGraphConfig) {
		cfg.Policy = CapathExactSequence
	})
	if _, err := exact.ValidatePath("CLIENT", "CORE", []string{"EDGE", "TRUSTED"}); err != nil {
		t.Fatalf("exact sequence: %v", err)
	}
	if _, err := exact.ValidatePath("CLIENT", "CORE", []string{"TRUSTED", "EDGE"}); !errors.Is(err, ErrCapathViolation) {
		t.Fatalf("exact sequence should reject reordering: %v", err)
	}
}

func TestCapathDirectOverride(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"}, {From: "B", To: "C"}, {From: "A", To: "C"},
	}, []CapathRule{{ClientRealm: "A", ServerRealm: "C", Intermediates: []string{"B"}}}, nil)
	allow := true
	if _, err := g.ValidatePathWithOptions("A", "C", nil, PathValidationOptions{AllowDirectWithCapath: &allow}); err != nil {
		t.Fatalf("direct override: %v", err)
	}
}

func TestPairwiseCapathBlocksShortcut(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"},
		{From: "B", To: "D"},
		{From: "A", To: "D"},
	}, []CapathRule{{ClientRealm: "A", ServerRealm: "D", Intermediates: []string{"B"}}}, nil)
	if _, err := g.ValidatePath("A", "D", nil); !errors.Is(err, ErrTransitShortcut) {
		t.Fatalf("direct A->D should be a shortcut: %v", err)
	}
	if _, err := g.ValidatePath("A", "D", []string{"B"}); err != nil {
		t.Fatalf("A->B->D should work: %v", err)
	}

	pairwise := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"}, {From: "B", To: "D"}, {From: "A", To: "D"},
	}, []CapathRule{{ClientRealm: "A", ServerRealm: "B", Intermediates: []string{"D"}}, {ClientRealm: "B", ServerRealm: "D", Intermediates: []string{"A"}}}, nil)
	if _, err := pairwise.ValidatePath("A", "D", nil); err != nil {
		t.Fatalf("unrelated full capath: %v", err)
	}
}

func TestPathEndpointStrippingAndLoops(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"}, {From: "B", To: "C"}, {From: "A", To: "D"}, {From: "D", To: "C"},
	}, nil, nil)
	if _, err := g.ValidatePath("A", "C", []string{"A", "B", "C"}); err != nil {
		t.Fatalf("redundant endpoints: %v", err)
	}
	if _, err := g.ValidatePath("A", "C", []string{"A", "B", "A", "C"}); !errors.Is(err, ErrRealmLoop) {
		t.Fatalf("interior repeated client: %v", err)
	}
	if _, err := g.ValidatePath("A", "C", []string{"B", "B"}); !errors.Is(err, ErrRealmLoop) {
		t.Fatalf("repeated intermediate: %v", err)
	}
}

func TestPathBoundsAndUnknownRealms(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"}, {From: "B", To: "C"}, {From: "C", To: "D"},
	}, nil, func(cfg *TrustGraphConfig) { cfg.MaxPathLength = 2 })
	if _, err := g.ValidatePath("A", "D", []string{"B", "C"}); !errors.Is(err, ErrPathLimit) {
		t.Fatalf("path limit: %v", err)
	}
	if _, err := g.ValidatePath("A", "C", []string{"UNKNOWN"}); !errors.Is(err, ErrPathNotAuthorized) {
		t.Fatalf("unknown realm: %v", err)
	}
	if _, err := g.ValidatePath("A", "UNKNOWN", nil); !errors.Is(err, ErrPathNotAuthorized) {
		t.Fatalf("unknown endpoint: %v", err)
	}
	if _, err := g.ValidatePath("A", "C", make([]string, 10)); !errors.Is(err, ErrPathLimit) {
		t.Fatalf("input length guard: %v", err)
	}
}

func TestValidateExpandedTrailingComma(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "CLIENT", To: "EDGE"}, {From: "EDGE", To: "CORE"},
	}, nil, nil)
	exp, err := ExpandTransited([]byte("EDGE,"), "CLIENT", "CORE", TransitedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.ValidateExpanded("CLIENT", "CORE", exp); !errors.Is(err, ErrIncompleteTransit) {
		t.Fatalf("trailing comma should be incomplete: %v", err)
	}
	exp, err = ExpandTransited([]byte("EDGE"), "CLIENT", "CORE", TransitedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.ValidateExpanded("CLIENT", "CORE", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.FullPath, []string{"CLIENT", "EDGE", "CORE"}) {
		t.Fatalf("FullPath = %v", got.FullPath)
	}
}

func TestGraphConfigurationErrors(t *testing.T) {
	cases := []func() error{
		func() error {
			_, err := NewTrustGraph(TrustGraphConfig{Edges: []TrustEdge{{From: "A", To: "A"}}})
			return err
		},
		func() error {
			_, err := NewTrustGraph(TrustGraphConfig{Capaths: []CapathRule{{ClientRealm: "A", ServerRealm: "A"}}})
			return err
		},
		func() error {
			_, err := NewTrustGraph(TrustGraphConfig{Capaths: []CapathRule{
				{ClientRealm: "A", ServerRealm: "B", Intermediates: []string{"C"}},
				{ClientRealm: "a", ServerRealm: "b", Intermediates: []string{"D"}},
			}})
			return err
		},
	}
	for i, fn := range cases {
		if err := fn(); err == nil {
			t.Fatalf("configuration case %d unexpectedly succeeded", i)
		}
	}
}

func TestGraphSuccessorsAndCapath(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{{From: "A", To: "B", Bidirectional: true}}, []CapathRule{
		{ClientRealm: "A", ServerRealm: "B", Intermediates: []string{}},
	}, nil)
	if !reflect.DeepEqual(g.Successors("A"), []string{"B"}) || !reflect.DeepEqual(g.Successors("B"), []string{"A"}) {
		t.Fatalf("successors = %v %v", g.Successors("A"), g.Successors("B"))
	}
	rule, ok := g.Capath("A", "B")
	if !ok || len(rule.Intermediates) != 0 {
		t.Fatalf("capath = %+v, %v", rule, ok)
	}
}

func ExampleTrustGraph() {
	g, _ := NewTrustGraph(TrustGraphConfig{
		Edges: []TrustEdge{
			{From: "EDGE", To: "CORE"},
		},
		Capaths: []CapathRule{
			{ClientRealm: "EDGE", ServerRealm: "CORE", Intermediates: nil},
		},
	})
	path, err := g.ValidatePath("EDGE", "CORE", nil)
	fmt.Printf("%v %v\n", path.FullPath, err)
	// Output: [EDGE CORE] <nil>
}
