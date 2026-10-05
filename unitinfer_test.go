package unitinfer

import (
	"math/big"
	"testing"
)

var dimNames = []string{"L", "M", "T"}

func rat(i int64) *big.Rat { return big.NewRat(i, 1) }

func checkConst(t *testing.T, sol *Solution, v int, want ...int64) {
	t.Helper()
	av, ok := sol.UnitOf(v)
	if !ok {
		t.Fatalf("var %d not in solution", v)
	}
	for d, w := range want {
		if av.Const[d].Cmp(rat(w)) != 0 {
			t.Fatalf("var %d dim %d: got %s, want %d (unit %s)",
				v, d, av.Const[d].RatString(), w, sol.UnitString(v, dimNames))
		}
	}
	if sol.IsFree(v) {
		t.Fatalf("var %d unexpectedly free: %s", v, sol.UnitString(v, dimNames))
	}
}

// E = m * v^2 with m: M and v: L/T must infer L^2 * M * T^-2.
func TestKineticEnergy(t *testing.T) {
	b := NewBuilder()
	m := b.Var("m")
	v := b.Var("v")
	e := b.Bin(OpMul, m, b.Pow(v, 2))

	in := NewInferencer(3)
	rv := in.Infer(e)
	in.eq(in.vt(in.UnitVar(m)), BaseTerm(0, 1, 0), m.ID(), "m is a mass")
	in.eq(in.vt(in.UnitVar(v)), BaseTerm(1, 0, -1), v.ID(), "v is a velocity")

	sol, ok := Solve(3, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	if sol.NumParams() != 0 {
		t.Fatalf("expected fully determined, got %d free params", sol.NumParams())
	}
	checkConst(t, sol, rv, 2, 1, -2)
}

// add(x, y) with unknown units: the most general solution must keep a free
// parameter instead of forcing dimensionless, and enumeration over a small
// exponent domain must match the parametric solution set exactly.
func TestUnderdeterminedStaysFree(t *testing.T) {
	b := NewBuilder()
	x := b.Var("x")
	y := b.Var("y")
	e := b.Bin(OpAdd, x, y)

	in := NewInferencer(2)
	rv := in.Infer(e)

	sol, ok := Solve(2, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	for _, v := range []int{in.UnitVar(x), in.UnitVar(y), rv} {
		if !sol.IsFree(v) {
			t.Fatalf("var %d must stay free, got %s", v, sol.UnitString(v, dimNames))
		}
	}
	rep, err := CheckInclusion(sol, in.Constraints(), -2, 2)
	if err != nil {
		t.Fatalf("inclusion check: %v", err)
	}
	if len(rep.Collapsed) != 0 {
		t.Fatalf("underdetermined vars collapsed to a single value: %v", rep.Collapsed)
	}
	// In particular x must range over non-dimensionless values too.
	xv := in.UnitVar(x)
	nonZero := false
	for _, s := range rep.Solutions {
		for i, v := range rep.Vars {
			if v != xv {
				continue
			}
			for d := 0; d < rep.Dims; d++ {
				if s[i*rep.Dims+d] != 0 {
					nonZero = true
				}
			}
		}
	}
	if !nonZero {
		t.Fatal("x was effectively pinned to dimensionless")
	}
}

// x * 1L with unknown x: result and x stay linked by one free parameter;
// enumeration must agree with the parametric solution.
func TestMulWithUnknownEnum(t *testing.T) {
	b := NewBuilder()
	x := b.Var("x")
	e := b.Bin(OpMul, x, b.Num(BaseTerm(1, 0)))

	in := NewInferencer(2)
	rv := in.Infer(e)

	sol, ok := Solve(2, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	if !sol.IsFree(in.UnitVar(x)) || !sol.IsFree(rv) {
		t.Fatal("x and result must remain parametric")
	}
	if _, err := CheckInclusion(sol, in.Constraints(), -2, 2); err != nil {
		t.Fatalf("inclusion check: %v", err)
	}
}

// A unit-polymorphic signature sq: α -> α^2 instantiated at different units.
func TestPolymorphicSignature(t *testing.T) {
	sq := &FuncSig{
		Name:   "sq",
		NVars:  1,
		Params: []Term{VarTerm(3, 0)},
		Result: VarTerm(3, 0).Scale(2),
	}
	b := NewBuilder()
	v := b.Var("v")
	m := b.Var("m")
	e1 := b.Call(sq, v)
	e2 := b.Call(sq, m)

	in := NewInferencer(3)
	r1 := in.Infer(e1)
	r2 := in.Infer(e2)
	in.eq(in.vt(in.UnitVar(v)), BaseTerm(1, 0, -1), v.ID(), "velocity")
	in.eq(in.vt(in.UnitVar(m)), BaseTerm(0, 1, 0), m.ID(), "mass")

	sol, ok := Solve(3, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	checkConst(t, sol, r1, 2, 0, -2)
	checkConst(t, sol, r2, 0, 2, 0)
}

// pow(x, 2) unified with L forces x = L^(1/2): rational particular solution.
func TestFractionalExponent(t *testing.T) {
	b := NewBuilder()
	x := b.Var("x")
	e := b.Bin(OpAdd, b.Pow(x, 2), b.Num(BaseTerm(1, 0, 0)))

	in := NewInferencer(3)
	in.Infer(e)

	sol, ok := Solve(3, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	av, _ := sol.UnitOf(in.UnitVar(x))
	if av.Const[0].Cmp(big.NewRat(1, 2)) != 0 {
		t.Fatalf("expected L^(1/2), got %s", sol.UnitString(in.UnitVar(x), dimNames))
	}
}

// A conflict localized in one subexpression must yield a minimal conflict
// set that excludes unrelated nodes.
func TestMinimalConflict(t *testing.T) {
	b := NewBuilder()
	a := b.Var("a")
	bv := b.Var("b")
	c := b.Var("c")
	d := b.Var("d")
	inner := b.Bin(OpAdd, b.Bin(OpMul, a, bv), c)
	root := b.Bin(OpSub, inner, d)

	in := NewInferencer(3)
	in.Infer(root)
	in.eq(in.vt(in.UnitVar(a)), BaseTerm(1, 0, 0), a.ID(), "a is a length")
	in.eq(in.vt(in.UnitVar(bv)), BaseTerm(0, 0, 1), bv.ID(), "b is a time")
	in.eq(in.vt(in.UnitVar(c)), BaseTerm(0, 1, 0), c.ID(), "c is a mass")
	// d is deliberately left unconstrained: the only source of the
	// conflict is a*b (L*T) being added to c (M), so the MUS is unique.

	cons := in.Constraints()
	if Consistent(3, cons, in.Vars()) {
		t.Fatal("expected inconsistent system")
	}
	core := MinimalConflict(3, cons, in.Vars())
	if len(core) == 0 || len(core) >= len(cons) {
		t.Fatalf("expected a proper non-empty core, got %d of %d", len(core), len(cons))
	}
	// Minimality: removing any single constraint restores consistency.
	for i := range core {
		rest := make([]Constraint, 0, len(core)-1)
		rest = append(rest, core[:i]...)
		rest = append(rest, core[i+1:]...)
		if !Consistent(3, rest, in.Vars()) {
			t.Fatal("core is not minimal")
		}
	}
	// The unrelated variable d and the outer subtraction must not be
	// part of the minimal conflicting expression set.
	nodes := ConflictNodes(core)
	for _, n := range nodes {
		if n == d.ID() || n == root.ID() {
			t.Fatalf("unrelated node #%d leaked into conflict set %v", n, nodes)
		}
	}
	want := map[int]bool{a.ID(): true, bv.ID(): true, c.ID(): true, inner.L.ID(): true, inner.ID(): true}
	if len(nodes) != len(want) {
		t.Fatalf("conflict nodes %v, want exactly %v", nodes, want)
	}
	for _, n := range nodes {
		if !want[n] {
			t.Fatalf("unexpected node #%d in conflict set %v", n, nodes)
		}
	}
}

// Division and subtraction of exponents: (a / b) with a: L^2, b: T gives L^2/T.
func TestDiv(t *testing.T) {
	b := NewBuilder()
	a := b.Var("a")
	tm := b.Var("t")
	e := b.Bin(OpDiv, a, tm)

	in := NewInferencer(3)
	rv := in.Infer(e)
	in.eq(in.vt(in.UnitVar(a)), BaseTerm(2, 0, 0), a.ID(), "area")
	in.eq(in.vt(in.UnitVar(tm)), BaseTerm(0, 0, 1), tm.ID(), "time")

	sol, ok := Solve(3, in.Constraints(), in.Vars())
	if !ok {
		t.Fatal("system unexpectedly inconsistent")
	}
	checkConst(t, sol, rv, 2, 0, -1)
}
