package ratls

import (
	"math/big"
	"testing"
)

func mustMat(t *testing.T, rows, cols int, entries ...string) *Matrix {
	t.Helper()
	m, err := ParseMatrix(rows, cols, entries...)
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	return m
}

func mustVec(t *testing.T, entries ...string) []*big.Rat {
	t.Helper()
	v, err := ParseVector(entries...)
	if err != nil {
		t.Fatalf("ParseVector: %v", err)
	}
	return v
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational " + s)
	}
	return r
}

func expectVec(t *testing.T, name string, got []*big.Rat, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got length %d, want %d (%v)", name, len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Cmp(rat(w)) != 0 {
			t.Fatalf("%s[%d]: got %s, want %s", name, i, got[i].RatString(), w)
		}
	}
}

func solveAndVerify(t *testing.T, A *Matrix, b []*big.Rat, budgetBits int) *Solution {
	t.Helper()
	sol, err := LeastSquares(A, b, budgetBits)
	if err != nil {
		t.Fatalf("LeastSquares: %v", err)
	}
	if err := Verify(A, b, sol, budgetBits); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return sol
}

// Full column rank, inconsistent b: the classic straight-line fit.
func TestFullRankOverdetermined(t *testing.T) {
	A := mustMat(t, 3, 2,
		"1", "1",
		"1", "2",
		"1", "3")
	b := mustVec(t, "1", "2", "2")
	sol := solveAndVerify(t, A, b, 0)
	if sol.Rank != 2 {
		t.Fatalf("rank: got %d, want 2", sol.Rank)
	}
	if len(sol.NullBasis) != 0 {
		t.Fatalf("null basis should be empty, got %d vectors", len(sol.NullBasis))
	}
	expectVec(t, "particular", sol.Particular, "2/3", "1/2")
	expectVec(t, "minnorm", sol.MinNorm, "2/3", "1/2")
}

// Two identical columns make A^T A singular; the affine family must
// capture every minimizer.
func TestDuplicateColumns(t *testing.T) {
	A := mustMat(t, 3, 2,
		"1", "1",
		"2", "2",
		"3", "3")
	b := mustVec(t, "1", "2", "3")
	sol := solveAndVerify(t, A, b, 0)
	if sol.Rank != 1 {
		t.Fatalf("rank: got %d, want 1", sol.Rank)
	}
	if len(sol.NullBasis) != 1 {
		t.Fatalf("null basis: got %d vectors, want 1", len(sol.NullBasis))
	}
	expectVec(t, "particular", sol.Particular, "1", "0")
	expectVec(t, "null[0]", sol.NullBasis[0], "-1", "1")
	// Min-norm point of {x : x1 + x2 = 1} is (1/2, 1/2).
	expectVec(t, "minnorm", sol.MinNorm, "1/2", "1/2")
}

// An all-zero column yields a free variable that the min-norm
// solution must set to zero.
func TestAllZeroColumn(t *testing.T) {
	A := mustMat(t, 2, 2,
		"1", "0",
		"2", "0")
	b := mustVec(t, "1", "2")
	sol := solveAndVerify(t, A, b, 0)
	if sol.Rank != 1 {
		t.Fatalf("rank: got %d, want 1", sol.Rank)
	}
	expectVec(t, "particular", sol.Particular, "1", "0")
	expectVec(t, "null[0]", sol.NullBasis[0], "0", "1")
	expectVec(t, "minnorm", sol.MinNorm, "1", "0")
}

// Zero samples: A is 0 x n, every x minimizes, min-norm is 0.
func TestNoSamples(t *testing.T) {
	A := NewMatrix(0, 3)
	b := []*big.Rat{}
	sol := solveAndVerify(t, A, b, 0)
	if sol.Rank != 0 {
		t.Fatalf("rank: got %d, want 0", sol.Rank)
	}
	if len(sol.NullBasis) != 3 {
		t.Fatalf("null basis: got %d vectors, want 3", len(sol.NullBasis))
	}
	for j, v := range sol.NullBasis {
		for i, x := range v {
			want := int64(0)
			if i == j {
				want = 1
			}
			if x.Cmp(big.NewRat(want, 1)) != 0 {
				t.Fatalf("null[%d][%d]: got %s, want %d", j, i, x.RatString(), want)
			}
		}
	}
	expectVec(t, "particular", sol.Particular, "0", "0", "0")
	expectVec(t, "minnorm", sol.MinNorm, "0", "0", "0")
}

// Large coprime denominators: an exactly consistent system whose
// solution has ~20-bit numerators and ~40-bit denominators must be
// recovered exactly, with no floating-point detour.
func TestLargeDenominators(t *testing.T) {
	xTrue := []*big.Rat{rat("1/1000003"), rat("-2/999983")}
	A := mustMat(t, 4, 2,
		"1", "1/7",
		"2/3", "1/11",
		"3/5", "-2/13",
		"1/2", "5/17")
	b, err := matVec(A, xTrue, budget{})
	if err != nil {
		t.Fatalf("matVec: %v", err)
	}
	sol := solveAndVerify(t, A, b, 0)
	if len(sol.NullBasis) != 0 {
		t.Fatalf("expected full rank, got %d null vectors", len(sol.NullBasis))
	}
	if !vecEqual(sol.MinNorm, xTrue) {
		t.Fatalf("minnorm: got [%s %s], want [%s %s]",
			sol.MinNorm[0].RatString(), sol.MinNorm[1].RatString(),
			xTrue[0].RatString(), xTrue[1].RatString())
	}
	if !vecEqual(sol.Particular, xTrue) {
		t.Fatalf("particular does not recover the exact solution")
	}
}

// The bit-length budget must abort the computation once intermediate
// coefficients grow past the limit, and succeed when it is generous.
func TestBudgetControl(t *testing.T) {
	xTrue := []*big.Rat{rat("1/1000003"), rat("-2/999983")}
	A := mustMat(t, 4, 2,
		"1", "1/7",
		"2/3", "1/11",
		"3/5", "-2/13",
		"1/2", "5/17")
	b, err := matVec(A, xTrue, budget{})
	if err != nil {
		t.Fatalf("matVec: %v", err)
	}

	_, err = LeastSquares(A, b, 8)
	if err == nil {
		t.Fatalf("expected budget error with 8-bit budget")
	}
	if _, ok := err.(*BudgetError); !ok {
		t.Fatalf("expected *BudgetError, got %T: %v", err, err)
	}

	sol, err := LeastSquares(A, b, 1<<20)
	if err != nil {
		t.Fatalf("generous budget should succeed: %v", err)
	}
	if !vecEqual(sol.MinNorm, xTrue) {
		t.Fatalf("minnorm mismatch under generous budget")
	}
}

// Every point of the affine family must satisfy the normal equations,
// and the min-norm point must be the unique norm minimizer on it.
func TestAffineFamilyAndMinNormOptimality(t *testing.T) {
	A := mustMat(t, 4, 3,
		"1", "1", "2",
		"2", "2", "4",
		"1", "1", "2",
		"3", "3", "6")
	b := mustVec(t, "1", "0", "2", "1")
	sol := solveAndVerify(t, A, b, 0)
	if len(sol.NullBasis) != 2 {
		t.Fatalf("null basis: got %d vectors, want 2", len(sol.NullBasis))
	}

	at := transpose(A)
	ata, err := matMul(at, A, budget{})
	if err != nil {
		t.Fatal(err)
	}
	atb, err := matVec(at, b, budget{})
	if err != nil {
		t.Fatal(err)
	}

	normSq := func(v []*big.Rat) *big.Rat {
		s := new(big.Rat)
		for _, x := range v {
			s.Add(s, new(big.Rat).Mul(x, x))
		}
		return s
	}
	minNormSq := normSq(sol.MinNorm)

	// Sample rational coefficient combos (t1, t2) on the affine family.
	combos := [][2]string{{"0", "0"}, {"1", "0"}, {"0", "-3/7"}, {"5/2", "11/13"}, {"-4", "9"}}
	for _, c := range combos {
		t1, t2 := rat(c[0]), rat(c[1])
		x := copyVec(sol.Particular)
		for i := range x {
			term1 := new(big.Rat).Mul(sol.NullBasis[0][i], t1)
			term2 := new(big.Rat).Mul(sol.NullBasis[1][i], t2)
			x[i].Add(x[i], term1).Add(x[i], term2)
		}
		lhs, err := matVec(ata, x, budget{})
		if err != nil {
			t.Fatal(err)
		}
		if !vecEqual(lhs, atb) {
			t.Fatalf("affine point for t=%v violates normal equations", c)
		}
		if normSq(x).Cmp(minNormSq) < 0 {
			t.Fatalf("affine point for t=%v has smaller norm than MinNorm", c)
		}
	}
}

// Verify must reject a tampered solution.
func TestVerifyRejectsTampering(t *testing.T) {
	A := mustMat(t, 3, 2,
		"1", "1",
		"1", "2",
		"1", "3")
	b := mustVec(t, "1", "2", "2")
	sol := solveAndVerify(t, A, b, 0)

	bad := &Solution{
		Rank:       sol.Rank,
		Particular: copyVec(sol.Particular),
		NullBasis:  sol.NullBasis,
		MinNorm:    copyVec(sol.MinNorm),
	}
	bad.MinNorm[0] = rat("7/3") // no longer a least-squares solution
	if err := Verify(A, b, bad, 0); err == nil {
		t.Fatalf("Verify accepted a tampered MinNorm")
	}

	bad2 := &Solution{
		Rank:       sol.Rank,
		Particular: copyVec(sol.Particular),
		NullBasis:  [][]*big.Rat{mustVec(t, "1", "1")}, // not in null(A)
		MinNorm:    copyVec(sol.MinNorm),
	}
	if err := Verify(A, b, bad2, 0); err == nil {
		t.Fatalf("Verify accepted a bogus null basis")
	}
}

// Dimension mismatch between A and b must be rejected.
func TestDimensionMismatch(t *testing.T) {
	A := mustMat(t, 2, 2, "1", "0", "0", "1")
	if _, err := LeastSquares(A, mustVec(t, "1"), 0); err == nil {
		t.Fatalf("expected dimension mismatch error")
	}
}
