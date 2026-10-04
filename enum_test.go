package unify

import "testing"

// enumTypes enumerates all type trees over flexible vars {a, b} and rigid
// var {r} with the given fuel. fuel 0 yields only leaves; fuel n adds
// function and binary-tuple nodes whose children have fuel n-1.
func enumTypes(fuel int) []*Type {
	base := []*Type{V("a"), V("b"), R("r")}
	if fuel == 0 {
		return base
	}
	children := enumTypes(fuel - 1)
	out := append([]*Type{}, base...)
	for _, x := range children {
		for _, y := range children {
			out = append(out, Fn(x, y), Tup(x, y))
		}
	}
	return out
}

// TestEnumRecursiveRejected: for every enumerated compound type t and every
// flexible variable v occurring in t, unifying v = t must be rejected by the
// occurs check, with a non-empty conflict chain.
func TestEnumRecursiveRejected(t *testing.T) {
	checked := 0
	for _, ty := range enumTypes(2) {
		if ty.Kind != Fun && ty.Kind != Tuple {
			continue
		}
		for _, v := range Vars(ty) {
			s := NewSolver()
			c := s.Unify(V(v), ty, "recursive")
			if c == nil {
				t.Fatalf("recursive type accepted: %s = %s", v, ty)
			}
			if len(c.Steps) == 0 {
				t.Fatalf("occurs conflict without chain: %s = %s", v, ty)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no recursive cases enumerated")
	}
	t.Logf("rejected %d recursive equations", checked)
}

// TestEnumEquivalentRenaming: unifying any enumerated type with a
// consistently variable-renamed copy must succeed, and the result must be a
// pure, injective, idempotent renaming.
func TestEnumEquivalentRenaming(t *testing.T) {
	swap := map[string]string{"a": "b", "b": "a"}
	for _, ty := range enumTypes(2) {
		ren := Rename(ty, swap)
		s := NewSolver()
		if c := s.Unify(ty, ren, "renaming"); c != nil {
			t.Fatalf("renamed copy rejected for %s:\n%s", ty, c)
		}
		sub := s.Substitution()
		if !sub.Idempotent() {
			t.Fatalf("renaming subst not idempotent for %s: %s", ty, sub)
		}
		if !Equal(sub.Apply(ty), sub.Apply(ren)) {
			t.Fatalf("renamed copies not unified for %s", ty)
		}
		seen := map[string]string{}
		for x, img := range sub {
			if img.Kind != Var {
				t.Fatalf("renaming bound %s to non-variable %s (type %s)", x, img, ty)
			}
			if prev, dup := seen[img.Name]; dup {
				t.Fatalf("renaming not injective: %s and %s both map to %s", prev, x, img.Name)
			}
			seen[img.Name] = x
		}
	}
}

// TestEnumMGU: for every enumerated pair, a successful unification must
// produce an idempotent substitution that makes both sides equal and is
// stable under self-composition; a failure must carry a non-empty chain.
func TestEnumMGU(t *testing.T) {
	types := enumTypes(1)
	ok, failed := 0, 0
	for _, x := range types {
		for _, y := range types {
			s := NewSolver()
			c := s.Unify(x, y, "pair")
			if c != nil {
				if len(c.Steps) == 0 {
					t.Fatalf("conflict without chain for %s = %s", x, y)
				}
				failed++
				continue
			}
			sub := s.Substitution()
			if !sub.Idempotent() {
				t.Fatalf("subst not idempotent for %s = %s: %s", x, y, sub)
			}
			if !Equal(sub.Apply(x), sub.Apply(y)) {
				t.Fatalf("subst does not unify %s = %s: %s", x, y, sub)
			}
			if !EqualSubst(Compose(sub, sub), sub) {
				t.Fatalf("self-composition not idempotent for %s = %s: %s", x, y, sub)
			}
			ok++
		}
	}
	t.Logf("pairs: %d unified, %d rejected", ok, failed)
}

// TestEnumSelfUnify: every type unifies with itself, yielding the identity.
func TestEnumSelfUnify(t *testing.T) {
	for _, ty := range enumTypes(2) {
		s := NewSolver()
		if c := s.Unify(ty, ty, "self"); c != nil {
			t.Fatalf("self-unification failed for %s:\n%s", ty, c)
		}
		if sub := s.Substitution(); len(sub) != 0 {
			t.Fatalf("self-unification of %s produced %s", ty, sub)
		}
	}
}

// TestEnumRigidNeverBound: no successful unification over enumerated types
// may bind the rigid variable to anything; rigid only matches itself.
func TestEnumRigidNeverBound(t *testing.T) {
	for _, ty := range enumTypes(1) {
		if ty.Kind == Rigid {
			continue
		}
		s := NewSolver()
		if c := s.Unify(R("r"), ty, "rigid"); c == nil {
			t.Fatalf("rigid variable bound to %s", ty)
		}
		s = NewSolver()
		if c := s.Unify(ty, R("r"), "rigid"); c == nil {
			t.Fatalf("rigid variable bound to %s", ty)
		}
	}
}
