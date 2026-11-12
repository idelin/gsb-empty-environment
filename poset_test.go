package netsec

import "testing"

func TestNewPosetTransitiveClosureAndOrder(t *testing.T) {
	p, err := NewPoset(
		[]string{"public", "internal", "confidential", "quarantine"},
		[]PosetEdge{
			{Lower: "public", Upper: "internal"},
			{Lower: "internal", Upper: "confidential"},
		},
	)
	if err != nil {
		t.Fatalf("NewPoset: %v", err)
	}

	cases := []struct {
		a, b string
		want Relation
	}{
		{"public", "public", RelationEqual},
		{"public", "internal", RelationLess},
		{"public", "confidential", RelationLess}, // 传递边
		{"confidential", "internal", RelationGreater},
		{"internal", "quarantine", RelationIncomparable},
		{"public", "missing", RelationUnknown},
	}
	for _, tc := range cases {
		if got := p.Order(tc.a, tc.b); got != tc.want {
			t.Errorf("Order(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}

	if !p.Leq("internal", "confidential") || p.Leq("confidential", "internal") {
		t.Errorf("Leq transitive pair mismatch")
	}
}

func TestNewPosetRejectsCycle(t *testing.T) {
	_, err := NewPoset(
		[]string{"a", "b"},
		[]PosetEdge{{Lower: "a", Upper: "b"}, {Lower: "b", Upper: "a"}},
	)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestNewPosetRejectsUnknownEdgeLabel(t *testing.T) {
	if _, err := NewPoset([]string{"a"}, []PosetEdge{{Lower: "a", Upper: "b"}}); err == nil {
		t.Fatal("expected unknown label error, got nil")
	}
}
