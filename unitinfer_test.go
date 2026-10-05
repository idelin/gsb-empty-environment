package unitinfer

import (
	"strings"
	"testing"
)

var dims = []string{"L", "T"}

func mustSolve(t *testing.T, in *Infer) *Solution {
	t.Helper()
	res := in.Solve()
	if !res.OK {
		t.Fatalf("expected solvable, got conflict: %v", res.Conflict)
	}
	return res.Solution
}

func unitOf(t *testing.T, s *Solution, v int, lambda ...int) []int {
	t.Helper()
	u, err := s.UnitOf(v, lambda)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func eqVec(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// x + 1[L] forces x : L, fully determined.
func TestAddForcesUnits(t *testing.T) {
	in := NewInfer(2, dims)
	in.Gen(Add{Var{"x"}, Const{[]int{1, 0}}})
	s := mustSolve(t, in)
	if s.Underdetermined() {
		t.Fatalf("expected determined solution, got %v", s)
	}
	if u := unitOf(t, s, 0); !eqVec(u, []int{1, 0}) {
		t.Fatalf("x = %v, want L", u)
	}
	if err := in.Verify(s, -2, 2); err != nil {
		t.Fatal(err)
	}
}

// x*y : L is underdetermined: one degree of freedom must remain free,
// and must NOT be collapsed to dimensionless.
func TestMulUnderdetermined(t *testing.T) {
	in := NewInfer(2, dims)
	tm := in.Gen(Mul{Var{"x"}, Var{"y"}})
	in.ConstrainEq(tm, ConstTerm([]int{1, 0}), "result is a length")
	s := mustSolve(t, in)
	if !s.Underdetermined() {
		t.Fatalf("expected free directions, got %v", s)
	}
	// Some instance gives x a non-dimensionless, non-particular unit.
	found := false
	for l0 := -2; l0 <= 2 && !found; l0++ {
		for l1 := -2; l1 <= 2 && !found; l1++ {
			for l2 := -2; l2 <= 2 && !found; l2++ {
				u, err := s.UnitOf(0, []int{l0, l1, l2}[:len(s.Basis)])
				if err != nil {
					t.Fatal(err)
				}
				if !eqVec(u, []int{0, 0}) {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("x is pinned to dimensionless; underdetermined variable lost its freedom")
	}
	// Cross-check against brute force on the small exponent domain.
	if err := in.Verify(s, -2, 2); err != nil {
		t.Fatal(err)
	}
	// Exact enumeration count: x0 in [-1,2] (4 values, y0 = 1-x0 in box),
	// x1 in [-2,2] (5 values, y1 = -x1) => 20 solutions.
	a, b, n := buildSystem(in.Constraints(), 2, 2)
	if got := len(EnumerateSolutions(a, b, n, -2, 2)); got != 20 {
		t.Fatalf("enumerated %d solutions, want 20", got)
	}
}

// x + 1[L] together with x + 1[T] is inconsistent; the minimal conflict
// set must contain exactly the two contradictory equality requirements.
func TestMinimalConflict(t *testing.T) {
	in := NewInfer(2, dims)
	in.Gen(Add{Var{"x"}, Const{[]int{1, 0}}})
	in.Gen(Add{Var{"x"}, Const{[]int{0, 1}}})
	in.Gen(Mul{Var{"y"}, Var{"z"}}) // unrelated, consistent
	res := in.Solve()
	if res.OK {
		t.Fatal("expected conflict")
	}
	if len(res.Conflict) != 2 {
		t.Fatalf("conflict core = %v, want 2 constraints", res.Conflict)
	}
	for _, c := range res.Conflict {
		if !strings.Contains(c.Where, "operands must share a unit") {
			t.Fatalf("unexpected constraint in core: %q", c.Where)
		}
	}
	// Minimality: every proper subset is consistent.
	for i := range res.Conflict {
		sub := append([]Constraint{}, res.Conflict[:i]...)
		sub = append(sub, res.Conflict[i+1:]...)
		if !consistent(sub, 2, in.NumVars()) {
			t.Fatal("conflict core is not minimal")
		}
	}
}

// x^2 : L^2 determines x : L; y^2 : L has no integer solution (sqrt of a
// base dimension is not representable) and must be reported as a conflict.
func TestPow(t *testing.T) {
	in := NewInfer(2, dims)
	tp := in.Gen(Pow{Var{"x"}, 2})
	in.ConstrainEq(tp, ConstTerm([]int{2, 0}), "square is an area")
	s := mustSolve(t, in)
	if u := unitOf(t, s, 0); !eqVec(u, []int{1, 0}) {
		t.Fatalf("x = %v, want L", u)
	}

	in2 := NewInfer(2, dims)
	tp2 := in2.Gen(Pow{Var{"y"}, 2})
	in2.ConstrainEq(tp2, ConstTerm([]int{1, 0}), "square is a length")
	if res := in2.Solve(); res.OK {
		t.Fatal("y^2 = L must be unsolvable over integer exponents")
	}
}

// Unit-polymorphic signatures: square = forall a. a -> a^2, id = forall a. a -> a.
func TestPolymorphicSignatures(t *testing.T) {
	square := &Signature{
		Name: "square", NumVars: 1,
		Params: []Term{VarTerm(0)},
		Result: VarTerm(0).Scale(2),
	}
	id := &Signature{
		Name: "id", NumVars: 1,
		Params: []Term{VarTerm(0)},
		Result: VarTerm(0),
	}
	in := NewInfer(2, dims)
	// square(2[L]) + id(1[L^2]) : both sides must be L^2.
	in.Gen(Add{
		Call{square, []Expr{Const{[]int{1, 0}}}},
		Call{id, []Expr{Const{[]int{2, 0}}}},
	})
	s := mustSolve(t, in)
	if s.Underdetermined() {
		t.Fatalf("signature instantiation should be determined: %v", s)
	}
	if err := in.Verify(s, -2, 2); err != nil {
		t.Fatal(err)
	}
}

// sqrt-like signature: forall a. a^2 -> a, applied to an area gives a length.
func TestSqrtSignature(t *testing.T) {
	sqrt := &Signature{
		Name: "sqrt", NumVars: 1,
		Params: []Term{VarTerm(0).Scale(2)},
		Result: VarTerm(0),
	}
	in := NewInfer(2, dims)
	tr := in.Gen(Call{sqrt, []Expr{Const{[]int{2, 0}}}})
	in.ConstrainEq(tr, VarTerm(in.FreshVar("out")), "bind result")
	s := mustSolve(t, in)
	// "out" is the last variable.
	u := unitOf(t, s, in.NumVars()-1)
	if !eqVec(u, []int{1, 0}) {
		t.Fatalf("sqrt(L^2) = %v, want L", u)
	}
}

// L / t : L*T^-1 determines t : T.
func TestDiv(t *testing.T) {
	in := NewInfer(2, dims)
	td := in.Gen(Div{Const{[]int{1, 0}}, Var{"t"}})
	in.ConstrainEq(td, ConstTerm([]int{1, -1}), "result is a velocity")
	s := mustSolve(t, in)
	if u := unitOf(t, s, 0); !eqVec(u, []int{0, 1}) {
		t.Fatalf("t = %v, want T", u)
	}
	if err := in.Verify(s, -2, 2); err != nil {
		t.Fatal(err)
	}
}

// The verifier must reject a "solution" that lazily defaults the
// underdetermined variables of x*y : L to dimensionless.
func TestVerifyRejectsDimensionlessDefault(t *testing.T) {
	in := NewInfer(2, dims)
	tm := in.Gen(Mul{Var{"x"}, Var{"y"}})
	in.ConstrainEq(tm, ConstTerm([]int{1, 0}), "result is a length")
	bogus := &Solution{
		NumDims:    2,
		DimNames:   dims,
		VarNames:   []string{"x", "y"},
		Particular: []int{0, 0, 0, 0}, // everything dimensionless: wrong
	}
	if err := in.Verify(bogus, -2, 2); err == nil {
		t.Fatal("verifier accepted dimensionless defaulting of an underdetermined problem")
	}
	// Also wrong: valid particular solution but missing free directions.
	half := &Solution{
		NumDims:    2,
		DimNames:   dims,
		VarNames:   []string{"x", "y"},
		Particular: []int{1, 0, 0, 0}, // x=L, y=1 is A solution, but not all
	}
	if err := in.Verify(half, -2, 2); err == nil {
		t.Fatal("verifier accepted a solution missing its free directions")
	}
}

// A fully free variable (no constraints at all) stays free in both dims.
func TestFullyFreeVariable(t *testing.T) {
	in := NewInfer(2, dims)
	in.Gen(Mul{Var{"x"}, Const{nil}}) // x * 1: no constraint on x
	s := mustSolve(t, in)
	if !s.Underdetermined() {
		t.Fatal("x must remain completely free")
	}
	if err := in.Verify(s, -1, 1); err != nil {
		t.Fatal(err)
	}
	a, b, n := buildSystem(in.Constraints(), 2, 1)
	if got := len(EnumerateSolutions(a, b, n, -1, 1)); got != 9 {
		t.Fatalf("enumerated %d solutions, want 9", got)
	}
}

// Chained arithmetic: (x * 2[T]) / x must simplify to T with x free.
func TestCancellationKeepsFreedom(t *testing.T) {
	in := NewInfer(2, dims)
	te := in.Gen(Div{Mul{Var{"x"}, Const{[]int{0, 1}}}, Var{"x"}})
	in.ConstrainEq(te, ConstTerm([]int{0, 1}), "ratio is a time")
	s := mustSolve(t, in)
	if !s.Underdetermined() {
		t.Fatal("x cancels and must stay free")
	}
	if err := in.Verify(s, -2, 2); err != nil {
		t.Fatal(err)
	}
}
