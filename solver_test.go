package unify

import (
	"strings"
	"testing"
)

func mustUnify(t *testing.T, s *Solver, a, b *Type) {
	t.Helper()
	if c := s.Unify(a, b); c != nil {
		t.Fatalf("expected success, got conflict:\n%s", c)
	}
}

func mustConflict(t *testing.T, s *Solver, a, b *Type) *Conflict {
	t.Helper()
	c := s.Unify(a, b)
	if c == nil {
		t.Fatalf("expected conflict for %s = %s, got success", a, b)
	}
	return c
}

func TestFunctionAndTuple(t *testing.T) {
	s := NewSolver()
	// (α -> (β, γ)) = ((R! -> γ) -> δ)
	mustUnify(t, s,
		Fun(Var("α"), Tuple(Var("β"), Var("γ"))),
		Fun(Fun(Rigid("R"), Var("γ")), Var("δ")),
	)
	sub := s.Subst()
	if got, want := sub["α"].String(), "(R! -> γ)"; got != want {
		t.Errorf("α = %s, want %s", got, want)
	}
	if got, want := sub["δ"].String(), "(β, γ)"; got != want {
		t.Errorf("δ = %s, want %s", got, want)
	}
	if _, ok := sub["β"]; ok {
		t.Errorf("β should stay free, got %s", sub["β"])
	}
}

func TestRigidCannotBindToStructure(t *testing.T) {
	s := NewSolver()
	c := mustConflict(t, s, Rigid("R"), Fun(Var("α"), Var("β")))
	if !strings.Contains(c.Reason, "rigid") {
		t.Errorf("reason should mention rigid: %s", c.Reason)
	}
}

func TestRigidMismatch(t *testing.T) {
	s := NewSolver()
	c := mustConflict(t, s, Rigid("R"), Rigid("S"))
	if len(c.Chain) == 0 {
		t.Error("conflict chain must not be empty")
	}
	// Same rigid unifies fine.
	s2 := NewSolver()
	mustUnify(t, s2, Rigid("R"), Rigid("R"))
}

func TestFlexibleVarMayBindToRigid(t *testing.T) {
	s := NewSolver()
	mustUnify(t, s, Var("α"), Rigid("R"))
	if got := s.Subst()["α"].String(); got != "R!" {
		t.Errorf("α = %s, want R!", got)
	}
}

func TestOccursDirect(t *testing.T) {
	s := NewSolver()
	c := mustConflict(t, s, Var("α"), Fun(Var("α"), Rigid("R")))
	if !strings.Contains(c.Reason, "occurs") {
		t.Errorf("reason should mention occurs: %s", c.Reason)
	}
}

func TestOccursIndirect(t *testing.T) {
	s := NewSolver()
	mustUnify(t, s, Var("α"), Fun(Var("β"), Rigid("R")))
	c := mustConflict(t, s, Var("β"), Tuple(Var("α")))
	if !strings.Contains(c.Reason, "occurs") {
		t.Fatalf("reason should mention occurs: %s", c.Reason)
	}
	// The chain must explain how α reached β's structure.
	joined := strings.Join(c.Chain, "\n")
	if !strings.Contains(joined, "α = (β -> R!)") {
		t.Errorf("chain should contain the equation linking α to β's structure:\n%s", joined)
	}
}

func TestMGUEquivalentVarsRenameToRepresentative(t *testing.T) {
	s := NewSolver()
	// (α -> α) = (β -> γ): most general result identifies α, β, γ.
	mustUnify(t, s, Fun(Var("α"), Var("α")), Fun(Var("β"), Var("γ")))
	a := s.Resolve(Fun(Var("α"), Var("α")))
	b := s.Resolve(Fun(Var("β"), Var("γ")))
	if !Equal(a, b) {
		t.Fatalf("resolved sides differ: %s vs %s", a, b)
	}
	// Exactly one free representative remains.
	rep := a.Args[0]
	if rep.Kind != KVar {
		t.Fatalf("representative should be a variable, got %s", rep)
	}
	if !Equal(a.Args[1], rep) || !Equal(b.Args[0], rep) || !Equal(b.Args[1], rep) {
		t.Errorf("all of α, β, γ should rename to %s, got %s and %s", rep, a, b)
	}
}

func TestSubstIdempotentAndCompose(t *testing.T) {
	s := NewSolver()
	mustUnify(t, s, Var("α"), Fun(Var("β"), Rigid("R")))
	mustUnify(t, s, Var("β"), Tuple(Var("γ"), Var("γ")))
	sub := s.Subst()
	if !sub.Idempotent() {
		t.Fatalf("solver substitution must be idempotent: %v", sub)
	}
	target := Var("α")
	once := sub.Apply(target)
	twice := sub.Apply(once)
	if !Equal(once, twice) {
		t.Errorf("apply not idempotent: %s vs %s", once, twice)
	}
	self := sub.Compose(sub)
	if !EqualSubst(sub, self) {
		t.Errorf("s ∘ s = %v, want %v", self, sub)
	}
}

func TestComposeChains(t *testing.T) {
	s1 := Subst{"α": Var("β")}
	s2 := Subst{"β": Fun(Rigid("R"), Var("γ"))}
	composed := s1.Compose(s2)
	if got := composed["α"].String(); got != "(R! -> γ)" {
		t.Errorf("composed α = %s, want (R! -> γ)", got)
	}
	if got := composed["β"].String(); got != "(R! -> γ)" {
		t.Errorf("composed β = %s, want (R! -> γ)", got)
	}
}

func TestShortestConflictChain(t *testing.T) {
	s := NewSolver()
	mustUnify(t, s, Var("α"), Var("β"))
	mustUnify(t, s, Var("β"), Var("γ"))
	mustUnify(t, s, Var("α"), Var("γ")) // redundant: no new provenance edge
	mustUnify(t, s, Var("γ"), Rigid("R"))
	c := mustConflict(t, s, Var("α"), Rigid("S"))
	joined := strings.Join(c.Chain, "\n")
	for _, want := range []string{"α = β", "β = γ", "γ = R!"} {
		if !strings.Contains(joined, want) {
			t.Errorf("chain missing %q:\n%s", want, joined)
		}
	}
	// Shortest: exactly 3 explanation equations plus the final clash line.
	if len(c.Chain) != 4 {
		t.Errorf("chain length = %d, want 4:\n%s", len(c.Chain), joined)
	}
}

func TestPathCompressionPreservesProvenance(t *testing.T) {
	s := NewSolver()
	mustUnify(t, s, Var("α"), Var("β"))
	mustUnify(t, s, Var("β"), Var("γ"))
	mustUnify(t, s, Var("γ"), Var("δ"))
	mustUnify(t, s, Var("δ"), Rigid("R"))
	// Hammer finds to force path compression across the whole class.
	for i := 0; i < 50; i++ {
		s.Resolve(Var("α"))
		s.Resolve(Var("β"))
		s.Subst()
	}
	c := mustConflict(t, s, Var("α"), Rigid("S"))
	joined := strings.Join(c.Chain, "\n")
	for _, want := range []string{"α = β", "β = γ", "γ = δ", "δ = R!"} {
		if !strings.Contains(joined, want) {
			t.Errorf("after compression, chain missing %q:\n%s", want, joined)
		}
	}
}

func TestDeepStructuralConflictChain(t *testing.T) {
	s := NewSolver()
	// α = (β -> β); β = R!; then α = (S! -> S!) must fail at the leaves.
	mustUnify(t, s, Var("α"), Fun(Var("β"), Var("β")))
	mustUnify(t, s, Var("β"), Rigid("R"))
	c := mustConflict(t, s, Var("α"), Fun(Rigid("S"), Rigid("S")))
	joined := strings.Join(c.Chain, "\n")
	if !strings.Contains(joined, "α = (β -> β)") {
		t.Errorf("chain should explain α's structure:\n%s", joined)
	}
	if !strings.Contains(c.Reason, "rigid") {
		t.Errorf("reason should mention rigid clash: %s", c.Reason)
	}
}

func TestTupleArityMismatch(t *testing.T) {
	s := NewSolver()
	c := mustConflict(t, s, Tuple(Var("α")), Tuple(Var("α"), Var("β")))
	if !strings.Contains(c.Reason, "shape mismatch") {
		t.Errorf("reason = %s, want shape mismatch", c.Reason)
	}
}
