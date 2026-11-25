package capsum

import (
	"testing"

	"prescreen.local/ps-2237843e01/pathlang"
)

func pathlangBuildFrom(r *Result, comp string) (*pathlang.DFA, error) {
	return pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{comp})
}

func pathlangSubsetFrom(a, b *pathlang.DFA) bool { return pathlang.Subset(a, b) }

func pathlangIsEmptyIntersect(a, b *pathlang.DFA) bool {
	return pathlang.IsEmpty(pathlang.Intersect(a, b))
}

func TestValidationFailures(t *testing.T) {
	base := func() *Program {
		p := rootProgram()
		p.Domains = []Domain{{Name: "D", Grants: []Grant{grant("ROOT", GrantSelf, "a")}}}
		p.EntryNode = "main"
		p.EntryDomains = []string{"D"}
		p.Nodes = []string{"main"}
		return p
	}

	t.Run("unknown entry domain", func(t *testing.T) {
		p := base()
		p.EntryDomains = []string{"GHOST"}
		if _, err := Analyze(p, 0); err == nil {
			t.Fatal("expected unknown entry domain error")
		}
	})

	t.Run("unknown root in grant", func(t *testing.T) {
		p := base()
		p.Domains[0].Grants[0].Root = "NOPE"
		if _, err := Analyze(p, 0); err == nil {
			t.Fatal("expected unknown root error")
		}
	})

	t.Run("non normalized component", func(t *testing.T) {
		p := base()
		p.Domains[0].Grants[0].Components = []string{".."}
		if _, err := Analyze(p, 0); err == nil {
			t.Fatal("expected normalization error")
		}
	})

	t.Run("handle budget", func(t *testing.T) {
		p := base()
		p.MaxHandles = 0
		p.Nodes = []string{"main", "q"}
		p.Edges = []Edge{{ID: "p", From: "main", To: "q", Kind: EdgePost, Handle: "H"}}
		if _, err := Analyze(p, 0); err == nil {
			t.Fatal("expected handle budget error")
		}
	})

	t.Run("unmatched call pair", func(t *testing.T) {
		p := base()
		p.Nodes = []string{"main", "c"}
		p.Edges = []Edge{{ID: "call", From: "main", To: "c", Kind: EdgeCall, Domains: []string{"D"}, PairID: "x"}}
		if _, err := Analyze(p, 0); err == nil {
			t.Fatal("expected unmatched call pair error")
		}
	})
}

// Nested posts inside a task must freeze a sub-language of the parent freeze,
// so a child task can never authorize resources outside the parent's bound.
func TestNestedTaskCannotEscape(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "Outer", Grants: []Grant{grant("ROOT", GrantDesc, "outer")}},
		{Name: "Inner", Grants: []Grant{
			grant("ROOT", GrantDesc, "outer"),
			grant("ROOT", GrantDesc, "escape"),
		}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"Outer"}
	p.Nodes = []string{"main", "posted", "consumer", "task", "nested", "done"}
	p.Edges = []Edge{
		{ID: "postOuter", From: "main", To: "posted", Kind: EdgePost, Handle: "H1"},
		{ID: "lc", From: "posted", To: "consumer", Kind: EdgeLocal},
		{ID: "consOuter", From: "consumer", To: "task", Kind: EdgeConsume, Handle: "H1", ConsumePairID: "c1"},
		{ID: "postInner", From: "task", To: "nested", Kind: EdgePost, Handle: "H2"},
		{ID: "innerLocal", From: "nested", To: "task", Kind: EdgeLocal},
		{ID: "tr1", From: "task", To: "done", Kind: EdgeTaskReturn, ConsumePairID: "c1"},
	}

	r, err := Analyze(p, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	outer, _ := pathlangBuildFrom(r, "outer")
	escape, _ := pathlangBuildFrom(r, "escape")

	h2 := r.Handles["H2"]
	if h2 == nil || !h2.Present {
		t.Fatal("nested handle missing")
	}
	if !pathlangSubsetFrom(h2.May, outer) {
		t.Fatal("nested freeze escaped the outer region")
	}
	if !pathlangIsEmptyIntersect(h2.May, escape) {
		t.Fatal("nested task freeze contains an escaping region")
	}
}
