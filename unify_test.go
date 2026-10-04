package unify

import (
	"strings"
	"testing"
)

func mustUnify(t *testing.T, s *Solver, a, b *Type, note string) {
	t.Helper()
	if c := s.Unify(a, b, note); c != nil {
		t.Fatalf("expected success, got conflict:\n%s", c)
	}
}

func TestBasicFunctionTuple(t *testing.T) {
	s := NewSolver()
	a, b := V("a"), V("b")
	mustUnify(t, s, Fn(a, b), Fn(Tup(V("c"), V("c")), a), "shape")
	sub := s.Substitution()
	if !sub.Idempotent() {
		t.Fatalf("substitution not idempotent: %s", sub)
	}
	// a = (c, c) after resolution, and b aliases a
	img := sub.Apply(a)
	want := Tup(V("c"), V("c"))
	if !Equal(img, want) {
		t.Fatalf("a resolved to %s, want %s", img, want)
	}
	if !Equal(sub.Apply(b), want) {
		t.Fatalf("b resolved to %s, want %s", sub.Apply(b), want)
	}
}

func TestMostGeneralUnifier(t *testing.T) {
	s := NewSolver()
	lhs := Fn(V("a"), Fn(V("b"), V("b")))
	rhs := Fn(Fn(V("c"), V("c")), V("d"))
	mustUnify(t, s, lhs, rhs, "mgu")
	sub := s.Substitution()
	gotL, gotR := sub.Apply(lhs), sub.Apply(rhs)
	if !Equal(gotL, gotR) {
		t.Fatalf("substitution does not unify: %s vs %s", gotL, gotR)
	}
	// MGU keeps b and c free: a := c->c, d := b->b
	want := Fn(Fn(V("c"), V("c")), Fn(V("b"), V("b")))
	if !Equal(gotL, want) {
		t.Fatalf("not most general: got %s, want %s", gotL, want)
	}
	if !EqualSubst(Compose(sub, sub), sub) {
		t.Fatalf("composition with itself not idempotent: %s", Compose(sub, sub))
	}
}

func TestOccursDirect(t *testing.T) {
	s := NewSolver()
	c := s.Unify(V("a"), Fn(V("a"), V("b")), "recursive")
	if c == nil {
		t.Fatal("expected occurs-check conflict")
	}
	if !strings.Contains(c.Reason, "occurs") {
		t.Fatalf("unexpected reason: %s", c.Reason)
	}
	if len(c.Steps) != 1 {
		t.Fatalf("expected 1-step chain, got %d:\n%s", len(c.Steps), c)
	}
}

func TestOccursChainSurvivesPathCompression(t *testing.T) {
	s := NewSolver()
	const n = 10
	vars := make([]*Type, n+1)
	for i := range vars {
		vars[i] = V("x" + string(rune('a'+i)))
	}
	for i := 0; i < n; i++ {
		mustUnify(t, s, vars[i], vars[i+1], "link")
	}
	// Hammer find() to force path compression of the whole chain.
	for i := 0; i < 3; i++ {
		s.find(vars[0])
	}
	c := s.Unify(vars[0], Fn(vars[n], vars[n]), "recursive")
	if c == nil {
		t.Fatal("expected occurs-check conflict")
	}
	// Chain must still traverse every link eq#1..eq#10 plus the final eq#11,
	// proving path compression did not destroy the explanation source.
	if len(c.Steps) != n+1 {
		t.Fatalf("expected %d steps, got %d:\n%s", n+1, len(c.Steps), c)
	}
	for i, st := range c.Steps {
		if st.Eq.ID != i+1 {
			t.Fatalf("step %d has eq#%d, want eq#%d\n%s", i, st.Eq.ID, i+1, c)
		}
	}
	if !strings.Contains(c.Detail, "arg0") {
		t.Fatalf("occurrence position missing from detail: %s", c.Detail)
	}
}

func TestRigidVariables(t *testing.T) {
	// rigid vs rigid (same name): ok
	s := NewSolver()
	mustUnify(t, s, R("r"), R("r"), "same rigid")

	// rigid vs different rigid: clash
	s = NewSolver()
	if c := s.Unify(R("r"), R("s"), "rigid clash"); c == nil {
		t.Fatal("expected rigid/rigid clash")
	}

	// rigid vs compound: clash
	s = NewSolver()
	c := s.Unify(R("r"), Fn(V("a"), V("a")), "rigid vs fun")
	if c == nil {
		t.Fatal("expected rigid/compound clash")
	}
	if !strings.Contains(c.Reason, "rigid") {
		t.Fatalf("unexpected reason: %s", c.Reason)
	}

	// flexible var vs rigid: clash (binding it would bind the rigid too)
	s = NewSolver()
	if c := s.Unify(V("a"), R("r"), "var vs rigid"); c == nil {
		t.Fatal("expected var/rigid clash")
	}

	// rigid deep inside a structure cannot be bound either
	s = NewSolver()
	mustUnify(t, s, V("a"), Fn(R("r"), V("b")), "bind shape")
	c = s.Unify(V("a"), Fn(Fn(V("c"), V("c")), V("b")), "rigid under")
	if c == nil {
		t.Fatal("expected deep rigid clash")
	}
	// The chain must explain how a became (!r -> b) (eq#4) before the
	// second equation (eq#5) forced !r against (c -> c).
	if len(c.Steps) != 3 {
		t.Fatalf("expected 3-step chain, got:\n%s", c)
	}
	if c.Steps[0].Eq.ID != 1 || c.Steps[2].Where != "arg0" {
		t.Fatalf("unexpected chain:\n%s", c)
	}
}

func TestConstructorMismatch(t *testing.T) {
	s := NewSolver()
	c := s.Unify(Fn(V("a"), V("a")), Tup(V("b"), V("b")), "fun vs tuple")
	if c == nil {
		t.Fatal("expected constructor mismatch")
	}
	if !strings.Contains(c.Reason, "constructor") {
		t.Fatalf("unexpected reason: %s", c.Reason)
	}

	s = NewSolver()
	if c := s.Unify(Tup(V("a"), V("b")), Tup(V("a"), V("b"), V("c")), "arity"); c == nil {
		t.Fatal("expected arity mismatch")
	}
}

func TestConflictChainThroughAliases(t *testing.T) {
	// a = b, b = (c -> c), then a = (d, d): the clash chain must explain
	// how a became a function type before meeting the tuple.
	s := NewSolver()
	mustUnify(t, s, V("a"), V("b"), "alias")
	mustUnify(t, s, V("b"), Fn(V("c"), V("c")), "shape")
	c := s.Unify(V("a"), Tup(V("d"), V("d")), "clash")
	if c == nil {
		t.Fatal("expected clash")
	}
	if len(c.Steps) != 3 {
		t.Fatalf("expected 3-step chain, got:\n%s", c)
	}
	ids := []int{c.Steps[0].Eq.ID, c.Steps[1].Eq.ID, c.Steps[2].Eq.ID}
	if ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("unexpected chain order %v:\n%s", ids, c)
	}
}

func TestRenamingResult(t *testing.T) {
	// Unifying a type with a consistently renamed copy must yield a pure
	// renaming (variable-to-variable) substitution.
	s := NewSolver()
	ty := Fn(V("a"), Tup(V("b"), V("a")))
	ren := Rename(ty, map[string]string{"a": "b", "b": "a"})
	mustUnify(t, s, ty, ren, "renaming")
	sub := s.Substitution()
	for x, img := range sub {
		if img.Kind != Var {
			t.Fatalf("%s bound to non-variable %s", x, img)
		}
	}
	if !Equal(sub.Apply(ty), sub.Apply(ren)) {
		t.Fatal("renamed copies not unified")
	}
}
