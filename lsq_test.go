package ratlsq

import (
	"math/big"
	"strings"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func mat(rows, cols int, entries ...string) *Matrix {
	if len(entries) != rows*cols {
		panic("bad entry count")
	}
	m := NewMatrix(rows, cols)
	for i, e := range entries {
		m.Set(i/cols, i%cols, rat(e))
	}
	return m
}

func vec(entries ...string) *Vector {
	v := NewVector(len(entries))
	for i, e := range entries {
		v.Set(i, rat(e))
	}
	return v
}

func normSquared(v *Vector) *big.Rat {
	acc := new(big.Rat)
	tmp := new(big.Rat)
	for i := 0; i < v.Len(); i++ {
		tmp.Mul(v.At(i), v.At(i))
		acc.Add(acc, tmp)
	}
	return acc
}

func residualSquared(a *Matrix, x, b *Vector) *big.Rat {
	ax, err := a.MulVec(x, Budget{})
	if err != nil {
		panic(err)
	}
	return normSquared(sub(ax, b))
}

// affine combines particular + t_i * basis_i.
func affine(sol *Solution, ts ...*big.Rat) *Vector {
	x := sol.Particular.Clone()
	tmp := new(big.Rat)
	for j, t := range ts {
		for i := 0; i < x.Len(); i++ {
			tmp.Mul(t, sol.NullBasis[j].At(i))
			tmp.Add(tmp, x.At(i))
			x.Set(i, tmp)
		}
	}
	return x
}

func solveAndVerify(t *testing.T, a *Matrix, b *Vector) *Solution {
	t.Helper()
	sol, err := LeastSquares(a, b, Budget{})
	if err != nil {
		t.Fatalf("LeastSquares: %v", err)
	}
	if err := Verify(a, b, sol, Budget{}); err != nil {
		t.Fatalf("Verify: %v\nsolution:\n%s", err, sol)
	}
	return sol
}

func TestUniqueSolution(t *testing.T) {
	// Fit a + b*t to (0,1), (1,2), (2,2): exact answer a=7/6, b=1/2.
	a := mat(3, 2, "1", "0", "1", "1", "1", "2")
	b := vec("1", "2", "2")
	sol := solveAndVerify(t, a, b)
	if sol.Rank != 2 {
		t.Fatalf("rank = %d, want 2", sol.Rank)
	}
	if len(sol.NullBasis) != 0 {
		t.Fatalf("expected unique solution, got nullity %d", len(sol.NullBasis))
	}
	want := vec("7/6", "1/2")
	if !sol.MinNorm.Equal(want) {
		t.Fatalf("MinNorm = %s, want %s", sol.MinNorm, want)
	}
	if !sol.Particular.Equal(want) {
		t.Fatalf("Particular = %s, want %s", sol.Particular, want)
	}
}

func TestDuplicateColumns(t *testing.T) {
	// Columns 0 and 1 identical, column 2 = col0 + col1: rank 1, nullity 2.
	a := mat(2, 3, "1", "1", "2", "2", "2", "4")
	b := vec("3", "6")
	sol := solveAndVerify(t, a, b)
	if sol.Rank != 1 || len(sol.NullBasis) != 2 {
		t.Fatalf("rank = %d, nullity = %d, want 1 and 2", sol.Rank, len(sol.NullBasis))
	}
	// Constraint is x0 + x1 + 2*x2 = 3; min norm is 3/6 * (1,1,2).
	want := vec("1/2", "1/2", "1")
	if !sol.MinNorm.Equal(want) {
		t.Fatalf("MinNorm = %s, want %s", sol.MinNorm, want)
	}
	// Every affine point is an exact least-squares solution with the
	// same residual.
	ts := [][2]string{{"0", "0"}, {"1", "0"}, {"0", "-3/5"}, {"7/2", "11/13"}, {"-4", "9"}}
	r0 := residualSquared(a, sol.MinNorm, b)
	for _, pair := range ts {
		x := affine(sol, rat(pair[0]), rat(pair[1]))
		if r := residualSquared(a, x, b); r.Cmp(r0) != 0 {
			t.Fatalf("residual at t=%v changed: %s != %s", pair, r, r0)
		}
		if got := normSquared(x); got.Cmp(normSquared(sol.MinNorm)) < 0 {
			t.Fatalf("affine point %s has smaller norm than MinNorm", x)
		}
	}
}

func TestAllZeroColumn(t *testing.T) {
	// Second column is entirely zero: x1 is a free parameter.
	a := mat(2, 2, "1", "0", "2", "0")
	b := vec("1", "2")
	sol := solveAndVerify(t, a, b)
	if sol.Rank != 1 || len(sol.NullBasis) != 1 {
		t.Fatalf("rank = %d, nullity = %d, want 1 and 1", sol.Rank, len(sol.NullBasis))
	}
	if !sol.NullBasis[0].Equal(vec("0", "1")) {
		t.Fatalf("null basis = %s, want (0, 1)", sol.NullBasis[0])
	}
	if !sol.MinNorm.Equal(vec("1", "0")) {
		t.Fatalf("MinNorm = %s, want (1, 0)", sol.MinNorm)
	}
}

func TestNoSamples(t *testing.T) {
	// 0 samples, 3 unknowns: every x in Q^3 is optimal; min norm is 0.
	a := NewMatrix(0, 3)
	b := NewVector(0)
	sol := solveAndVerify(t, a, b)
	if sol.Rank != 0 || len(sol.NullBasis) != 3 {
		t.Fatalf("rank = %d, nullity = %d, want 0 and 3", sol.Rank, len(sol.NullBasis))
	}
	if !sol.Particular.IsZero() || !sol.MinNorm.IsZero() {
		t.Fatalf("expected zero solutions, got %s and %s", sol.Particular, sol.MinNorm)
	}
	for i, v := range sol.NullBasis {
		want := NewVector(3)
		want.Set(i, big.NewRat(1, 1))
		if !v.Equal(want) {
			t.Fatalf("null basis vector %d = %s, want %s", i, v, want)
		}
	}
}

func TestNoUnknowns(t *testing.T) {
	a := NewMatrix(2, 0)
	b := vec("1", "2")
	sol := solveAndVerify(t, a, b)
	if sol.Rank != 0 || len(sol.NullBasis) != 0 || sol.MinNorm.Len() != 0 {
		t.Fatalf("unexpected solution shape: %s", sol)
	}
}

func TestLargeDenominators(t *testing.T) {
	// 1e6-scale denominators must survive exactly.
	a := mat(1, 1, "1/1000000")
	b := vec("1/3")
	sol := solveAndVerify(t, a, b)
	if !sol.MinNorm.Equal(vec("1000000/3")) {
		t.Fatalf("MinNorm = %s, want 1000000/3", sol.MinNorm)
	}

	// Mixed large coprime denominators; compare against an exact residual
	// computed independently.
	den := []string{"999983", "999979", "999961", "999959", "999937", "999907"}
	a2 := mat(3, 2,
		"1/"+den[0], "1/"+den[1],
		"1/"+den[2], "1/"+den[3],
		"1/"+den[4], "1/"+den[5])
	b2 := vec("1/7", "2/11", "3/13")
	sol2 := solveAndVerify(t, a2, b2)
	if len(sol2.NullBasis) != 0 {
		t.Fatalf("expected unique solution, got nullity %d", len(sol2.NullBasis))
	}
	// Perturbing the unique solution in any rational direction must
	// strictly increase the residual.
	r0 := residualSquared(a2, sol2.MinNorm, b2)
	for _, d := range [][2]string{{"1/3", "0"}, {"0", "-2/5"}, {"5/7", "11/3"}} {
		x := sol2.MinNorm.Clone()
		tmp := new(big.Rat)
		for i := 0; i < 2; i++ {
			tmp.Add(x.At(i), rat(d[i]))
			x.Set(i, tmp)
		}
		if r := residualSquared(a2, x, b2); r.Cmp(r0) <= 0 {
			t.Fatalf("perturbation %v did not increase residual: %s <= %s", d, r, r0)
		}
	}
}

func TestBudgetExceeded(t *testing.T) {
	// Denominators of 2^6: products reach 2^12, far beyond a 4-bit budget.
	a := mat(2, 1, "1/64", "1/64")
	b := vec("1", "1")
	_, err := LeastSquares(a, b, Budget{MaxBits: 4})
	if err == nil {
		t.Fatal("expected BudgetError, got nil")
	}
	be, ok := err.(*BudgetError)
	if !ok {
		t.Fatalf("error type = %T, want *BudgetError (%v)", err, err)
	}
	if be.Max != 4 || be.Bits <= 4 {
		t.Fatalf("unexpected BudgetError fields: %+v", be)
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error message should mention budget: %v", err)
	}
	// A generous budget succeeds and Verify also passes under it.
	sol, err := LeastSquares(a, b, Budget{MaxBits: 64})
	if err != nil {
		t.Fatalf("LeastSquares with ample budget: %v", err)
	}
	if err := Verify(a, b, sol, Budget{MaxBits: 256}); err != nil {
		t.Fatalf("Verify with ample budget: %v", err)
	}
	if !sol.MinNorm.Equal(vec("64")) {
		t.Fatalf("MinNorm = %s, want 64", sol.MinNorm)
	}
}

func TestBudgetGrowthIsExact(t *testing.T) {
	// With unlimited budget, huge intermediate coefficients are exact:
	// x = 1/(1/2^30 + 1/3^20) summed over two rows.
	a := mat(2, 1, "1/1073741824", "1/3486784401")
	b := vec("1", "1")
	sol, err := LeastSquares(a, b, Budget{})
	if err != nil {
		t.Fatalf("LeastSquares: %v", err)
	}
	// x = (1/p^2 + 1/q^2)^{-1} (1/p + 1/q) with p=2^30, q=3^20
	//    = p q (p + q) / (p^2 + q^2).
	p := big.NewInt(1073741824)
	q := big.NewInt(3486784401)
	num := new(big.Int).Mul(p, q)
	num.Mul(num, new(big.Int).Add(p, q))
	den := new(big.Int).Add(new(big.Int).Mul(p, p), new(big.Int).Mul(q, q))
	want := new(big.Rat).SetFrac(num, den)
	if sol.MinNorm.At(0).Cmp(want) != 0 {
		t.Fatalf("MinNorm = %s, want %s", sol.MinNorm.At(0), want)
	}
	if err := Verify(a, b, sol, Budget{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRankDeficientMinNormOrthogonal(t *testing.T) {
	// Row 2 = row 0 + row 1 and column 2 = column 0 - column 1.
	a := mat(4, 3,
		"1", "2", "-1",
		"0", "1", "-1",
		"1", "3", "-2",
		"2", "1", "1")
	b := vec("1", "0", "2", "3")
	sol := solveAndVerify(t, a, b)
	if len(sol.NullBasis) != 1 {
		t.Fatalf("nullity = %d, want 1", len(sol.NullBasis))
	}
	// MinNorm must achieve the minimal norm over the whole affine family.
	n0 := normSquared(sol.MinNorm)
	for _, ts := range []string{"-5/2", "-1", "0", "3/7", "4"} {
		x := affine(sol, rat(ts))
		if got := normSquared(x); got.Cmp(n0) < 0 {
			t.Fatalf("norm at t=%s is %s < min-norm %s", ts, got, n0)
		}
	}
}
