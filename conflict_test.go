package main

import "testing"

func TestEnumerateDomainVerifiesConflictScope(t *testing.T) {
	domain := map[string][]string{"pod": {"p1", "p2"}, "app": {"x", "y"}}
	all := EnumerateDomain("m", domain)
	if len(all) != 4 {
		t.Fatalf("domain expansion = %d series, want 4", len(all))
	}
	graph := BuildConflictGraph(all, []Rule{DeleteLabel{RuleID: "r9", Label: "pod"}})
	if len(graph.Nodes) != 2 {
		t.Fatalf("conflict nodes = %d, want 2 (one per app value)", len(graph.Nodes))
	}
	for _, n := range graph.Nodes {
		if len(n.Inputs) != 2 {
			t.Fatalf("node inputs = %v", n.Inputs)
		}
		if len(n.Rules) != 1 || n.Rules[0] != "r9" {
			t.Fatalf("node rules = %v", n.Rules)
		}
	}
	pair, ok := graph.MinimalPair()
	if !ok {
		t.Fatal("no minimal pair")
	}
	want := Series{Name: "m", Labels: LabelSet{"pod": "p1", "app": "x"}}.Key()
	if pair[0] != want {
		t.Fatalf("minimal pair = %v, want first %q", pair, want)
	}
}

func TestConflictGraphDynamicRetraction(t *testing.T) {
	all := EnumerateDomain("m", map[string][]string{"pod": {"p1", "p2"}})
	graph := BuildConflictGraph(all, []Rule{
		RenameLabel{RuleID: "r1", From: "pod", To: "instance"},
		DeleteLabel{RuleID: "r2", Label: "instance"},
	})
	if !graph.HasConflicts() {
		t.Fatal("combined rules should collide")
	}
	// Retract the delete: rename alone keeps series distinct.
	graph.Retract("r2")
	if graph.HasConflicts() {
		t.Fatal("after retracting r2 no conflict should remain")
	}
}

func TestOutputEqualityByLabelsNotText(t *testing.T) {
	a := LabelSet{"x": "1", "y": "2"}
	b := LabelSet{"y": "2", "x": "1"} // different construction order
	if a.Key() != b.Key() {
		t.Fatal("label sets equal by content must share a canonical key")
	}
	// Two different rewrite paths landing on the same labels form one node.
	s1 := Series{Name: "m", Labels: LabelSet{"job": "a", "svc": "keep"}}
	s2 := Series{Name: "m", Labels: LabelSet{"service": "a", "svc": "keep"}}
	graph := BuildConflictGraph([]Series{s1, s2}, []Rule{
		RenameLabel{RuleID: "r1", From: "job", To: "service"},
	})
	if len(graph.Nodes) != 1 {
		t.Fatalf("same output labels via different rewrites must merge into 1 node, got %d", len(graph.Nodes))
	}
}

func TestMultipleRuleCombinationCollision(t *testing.T) {
	s1 := Series{Name: "m", Labels: LabelSet{"a": "1", "b": "2", "c": "3"}}
	s2 := Series{Name: "m", Labels: LabelSet{"a": "1", "b": "9", "c": "8"}}
	// No single rule collides; only the combination does.
	graph := BuildConflictGraph([]Series{s1, s2}, []Rule{
		DeleteLabel{RuleID: "r1", Label: "b"},
		DeleteLabel{RuleID: "r2", Label: "c"},
	})
	if len(graph.Nodes) != 1 {
		t.Fatalf("combined deletes should collide, nodes=%d", len(graph.Nodes))
	}
	n := graph.Nodes[LabelSet{"a": "1"}.Key()]
	if n == nil || len(n.Rules) != 2 {
		t.Fatalf("node = %+v", n)
	}
}
