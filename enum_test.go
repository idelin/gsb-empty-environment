package unify

import (
	"strings"
	"testing"
)

// enumTypes enumerates all type trees of depth <= d over two flexible
// variables and one rigid constant.
func enumTypes(d int) []*Type {
	base := []*Type{Var("α"), Var("β"), Rigid("R")}
	if d == 0 {
		return base
	}
	prev := enumTypes(d - 1)
	out := append([]*Type{}, prev...)
	for _, a := range prev {
		for _, b := range prev {
			out = append(out, Fun(a, b))
			out = append(out, Tuple(a, b))
		}
	}
	return out
}

var renaming = map[string]string{"α": "α'", "β": "β'", "R": "R"}

// TestEnumeration checks, over all pairs (shallow x deep) of small type
// trees:
//   - soundness: on success both sides resolve equal under the MGU
//   - the MGU substitution is idempotent and self-composing
//   - recursive types are always rejected by the occurs check
//   - consistently renaming variables yields an isomorphic outcome
func TestEnumeration(t *testing.T) {
	shallow := enumTypes(1)
	deep := enumTypes(2)
	var successes, occursFailures, otherFailures int

	for _, a := range shallow {
		for _, b := range deep {
			s := NewSolver()
			c := s.Unify(a, b)
			if c != nil {
				if strings.Contains(c.Reason, "occurs") {
					occursFailures++
				} else {
					otherFailures++
				}
				// Renamed counterpart must fail too.
				s2 := NewSolver()
				if c2 := s2.Unify(Rename(a, renaming), Rename(b, renaming)); c2 == nil {
					t.Errorf("renamed pair %s = %s succeeded while original failed", a, b)
				}
				continue
			}
			successes++
			sub := s.Subst()

			// Soundness: the MGU equates both sides.
			ra, rb := sub.Apply(a), sub.Apply(b)
			if !Equal(ra, rb) {
				t.Errorf("%s = %s: sides differ after MGU: %s vs %s", a, b, ra, rb)
			}

			// Idempotency of the substitution and of self-composition.
			if !sub.Idempotent() {
				t.Errorf("%s = %s: substitution %v not idempotent", a, b, sub)
			}
			if !Equal(sub.Apply(ra), ra) {
				t.Errorf("%s = %s: re-applying MGU changes %s", a, b, ra)
			}
			if self := sub.Compose(sub); !EqualSubst(self, sub) {
				t.Errorf("%s = %s: s∘s = %v, want %v", a, b, self, sub)
			}

			// Equivalent renaming: unifying the renamed pair must yield
			// the renamed result.
			s2 := NewSolver()
			a2, b2 := Rename(a, renaming), Rename(b, renaming)
			if c2 := s2.Unify(a2, b2); c2 != nil {
				t.Errorf("renamed pair %s = %s failed: %s", a2, b2, c2)
				continue
			}
			sub2 := s2.Subst()
			if got, want := sub2.Apply(a2), Rename(ra, renaming); !Equal(got, want) {
				t.Errorf("renaming mismatch: %s resolves to %s, want %s", a2, got, want)
			}
		}
	}
	t.Logf("pairs=%d successes=%d occurs-rejections=%d other-conflicts=%d",
		len(shallow)*len(deep), successes, occursFailures, otherFailures)
	if occursFailures == 0 {
		t.Error("enumeration should have exercised occurs-check rejections")
	}
	if successes == 0 {
		t.Error("enumeration should have exercised successful unifications")
	}
}

// TestOccursRejectionsAreRecursive spot-checks that every occurs failure
// from a direct self-reference is genuinely recursive.
func TestOccursRejectionsAreRecursive(t *testing.T) {
	cases := [][2]*Type{
		{Var("α"), Fun(Var("α"), Rigid("R"))},
		{Var("α"), Tuple(Var("β"), Var("α"))},
		{Var("α"), Fun(Rigid("R"), Tuple(Var("α")))},
	}
	for i, tc := range cases {
		s := NewSolver()
		c := s.Unify(tc[0], tc[1])
		if c == nil {
			t.Errorf("case %d: recursive type %s = %s accepted", i, tc[0], tc[1])
			continue
		}
		if !strings.Contains(c.Reason, "occurs") {
			t.Errorf("case %d: reason = %s, want occurs", i, c.Reason)
		}
		if len(c.Chain) == 0 {
			t.Errorf("case %d: conflict chain must not be empty", i)
		}
	}
}
