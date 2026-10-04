package sparselu

import (
	"errors"
	"math/big"
	"testing"
)

// mustMat builds a matrix from rows of rational literals ("3/2", "-4", "0").
func mustMat(t *testing.T, rows ...[]string) *Matrix {
	t.Helper()
	m := NewMatrix(len(rows), len(rows[0]))
	for i, row := range rows {
		if len(row) != m.Cols {
			t.Fatalf("ragged matrix literal")
		}
		for j, s := range row {
			v, ok := new(big.Rat).SetString(s)
			if !ok {
				t.Fatalf("bad rational %q", s)
			}
			m.Set(i, j, v)
		}
	}
	return m
}

// checkFactorization verifies P*A*Q == L*U by independent sparse
// multiplication, plus the triangular shapes of L and U.
func checkFactorization(t *testing.T, a *Matrix, res *Factorization) {
	t.Helper()
	if res == nil {
		t.Fatal("nil factorization")
	}
	paq := Permute(a, res.P, res.Q)
	lu := Multiply(res.L, res.U)
	if !Equal(paq, lu) {
		t.Fatalf("PAQ != LU\nA:\n%sPAQ:\n%sLU:\n%sL:\n%sU:\n%s", a, paq, lu, res.L, res.U)
	}
	// L is unit lower triangular.
	for i := 0; i < res.L.Rows; i++ {
		if v := res.L.at(i, i); v == nil || v.Cmp(big.NewRat(1, 1)) != 0 {
			t.Fatalf("L[%d][%d] is not 1", i, i)
		}
		for j := i + 1; j < res.L.Cols; j++ {
			if res.L.at(i, j) != nil {
				t.Fatalf("L[%d][%d] nonzero above diagonal", i, j)
			}
		}
	}
	// U is upper trapezoidal with nonzero pivots on the first Rank rows.
	for i := 0; i < res.U.Rows; i++ {
		for j := 0; j < i && j < res.U.Cols; j++ {
			if res.U.at(i, j) != nil {
				t.Fatalf("U[%d][%d] nonzero below diagonal", i, j)
			}
		}
		if i < res.Rank && res.U.at(i, i) == nil {
			t.Fatalf("U[%d][%d] pivot missing within rank %d", i, i, res.Rank)
		}
	}
	// No stored zeros anywhere (no residue after cancellation).
	for name, m := range map[string]*Matrix{"L": res.L, "U": res.U} {
		for r, row := range m.rows {
			for c, v := range row {
				if v.Sign() == 0 {
					t.Fatalf("%s[%d][%d] holds a stored zero", name, r, c)
				}
			}
		}
	}
}

func isIdentity(p []int) bool {
	for i, v := range p {
		if v != i {
			return false
		}
	}
	return true
}

func TestBasicSquareFactorization(t *testing.T) {
	a := mustMat(t,
		[]string{"2", "1", "1"},
		[]string{"4", "3", "3"},
		[]string{"8", "7", "9"},
	)
	res := Factorize(a)
	if res.Rank != 3 {
		t.Fatalf("rank = %d, want 3", res.Rank)
	}
	checkFactorization(t, a, res)
}

func TestRationalsExact(t *testing.T) {
	a := mustMat(t,
		[]string{"1/2", "1/3"},
		[]string{"-2/5", "7/4"},
	)
	res := Factorize(a)
	checkFactorization(t, a, res)
	// Exact rational multiplier: L[1][0] = (-2/5)/(1/2) = -4/5.
	want := big.NewRat(-4, 5)
	if got := res.L.at(1, 0); got == nil || got.Cmp(want) != 0 {
		t.Fatalf("L[1][0] = %v, want %v", got, want)
	}
}

func TestRowSwapOnZeroPivot(t *testing.T) {
	// Row 1 of the trailing submatrix is entirely zero at step 1, so a
	// row swap is required; no column swap is needed.
	a := mustMat(t,
		[]string{"1", "1", "1"},
		[]string{"0", "0", "0"},
		[]string{"0", "1", "1"},
	)
	res := Factorize(a)
	if res.Rank != 2 {
		t.Fatalf("rank = %d, want 2", res.Rank)
	}
	if isIdentity(res.P) {
		t.Fatal("expected a row permutation for zero pivot row")
	}
	if !isIdentity(res.Q) {
		t.Fatalf("Q = %v, want identity (no column swap needed)", res.Q)
	}
	checkFactorization(t, a, res)
}

func TestColumnSwapChosenByFullPivoting(t *testing.T) {
	// The first nonzero of the trailing submatrix sits in a later column,
	// so full pivoting swaps columns instead of rows.
	a := mustMat(t,
		[]string{"0", "1"},
		[]string{"2", "3"},
	)
	res := Factorize(a)
	if res.Rank != 2 {
		t.Fatalf("rank = %d, want 2", res.Rank)
	}
	if isIdentity(res.P) && isIdentity(res.Q) {
		t.Fatal("expected a permutation for zero pivot")
	}
	checkFactorization(t, a, res)
}

// TestColumnSwapOnlyContinuation uses a rectangular matrix whose step-1
// pivot column is entirely zero in the trailing submatrix, so row swaps
// cannot help: only a column swap lets the factorization continue.
func TestColumnSwapOnlyContinuation(t *testing.T) {
	a := mustMat(t,
		[]string{"2", "0", "1", "0"},
		[]string{"0", "0", "3", "1"},
		[]string{"0", "0", "1", "2"},
	)
	// Sanity: the pivot column at step 1 has no nonzero in the trailing
	// submatrix, so row pivoting alone cannot proceed.
	if a.at(1, 1) != nil || a.at(2, 1) != nil {
		t.Fatal("test matrix must have a zero pivot column at step 1")
	}
	res := Factorize(a)
	if res.Rank != 3 {
		t.Fatalf("rank = %d, want 3", res.Rank)
	}
	if !isIdentity(res.P) {
		t.Fatalf("P = %v, want identity (no row swap can help)", res.P)
	}
	if isIdentity(res.Q) {
		t.Fatal("expected a column swap to continue past the zero pivot column")
	}
	checkFactorization(t, a, res)
}

func TestRankDeficientReportsRank(t *testing.T) {
	a := mustMat(t,
		[]string{"1", "2", "3"},
		[]string{"2", "4", "6"},
		[]string{"1", "1", "0"},
	)
	res := Factorize(a)
	if res.Rank != 2 {
		t.Fatalf("rank = %d, want 2", res.Rank)
	}
	checkFactorization(t, a, res)
}

func TestRectangularWide(t *testing.T) {
	a := mustMat(t,
		[]string{"1", "0", "3", "0", "1"},
		[]string{"0", "2", "1", "1", "0"},
		[]string{"1", "1", "0", "0", "2"},
	)
	res := Factorize(a)
	if res.Rank != 3 {
		t.Fatalf("rank = %d, want 3", res.Rank)
	}
	checkFactorization(t, a, res)
}

func TestRectangularTallRankDeficient(t *testing.T) {
	a := mustMat(t,
		[]string{"1", "2"},
		[]string{"2", "4"},
		[]string{"0", "1"},
		[]string{"3", "0"},
		[]string{"1", "1"},
	)
	res := Factorize(a)
	if res.Rank != 2 {
		t.Fatalf("rank = %d, want 2", res.Rank)
	}
	checkFactorization(t, a, res)
}

// TestBudgetExhaustionIsResumable runs the same matrix with a budget of one
// elimination per call and checks the factorization resumes from its saved
// state and matches the unlimited one-shot result.
func TestBudgetExhaustionIsResumable(t *testing.T) {
	a := mustMat(t,
		[]string{"2", "1", "0", "3"},
		[]string{"1", "2", "1", "0"},
		[]string{"0", "1", "2", "1"},
		[]string{"3", "0", "1", "2"},
	)
	want := Factorize(a)

	f := NewFactorizer(a)
	calls := 0
	for {
		err := f.Run(1)
		calls++
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBudgetExhausted) {
			t.Fatalf("unexpected error: %v", err)
		}
		if f.Done() {
			t.Fatal("Done before final Run call")
		}
		if f.Result() != nil {
			t.Fatal("Result available before completion")
		}
		if calls > 100 {
			t.Fatal("factorization did not converge under budget=1")
		}
	}
	if calls < 2 {
		t.Fatalf("budget never exhausted; calls = %d", calls)
	}
	got := f.Result()
	if got.Rank != want.Rank {
		t.Fatalf("rank = %d, want %d", got.Rank, want.Rank)
	}
	for i := range got.P {
		if got.P[i] != want.P[i] || got.Q[i] != want.Q[i] {
			t.Fatalf("permutations differ: P %v vs %v, Q %v vs %v", got.P, want.P, got.Q, want.Q)
		}
	}
	if !Equal(got.L, want.L) || !Equal(got.U, want.U) {
		t.Fatal("resumed factorization differs from one-shot result")
	}
	checkFactorization(t, a, got)
}

// TestBudgetZeroProgress verifies a zero budget makes no progress but leaves
// a valid resumable state.
func TestBudgetZeroProgress(t *testing.T) {
	a := mustMat(t,
		[]string{"1", "1"},
		[]string{"1", "2"},
	)
	f := NewFactorizer(a)
	if err := f.Run(0); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("Run(0) = %v, want ErrBudgetExhausted", err)
	}
	if err := f.Run(-1); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	checkFactorization(t, a, f.Result())
}

// TestCancellationLeavesNoResidualZeros uses exact cancellation: row 1 minus
// row 0 wipes out every entry, so the rank drops and no zeros remain stored.
func TestCancellationLeavesNoResidualZeros(t *testing.T) {
	a := mustMat(t,
		[]string{"1/2", "3/4"},
		[]string{"1/2", "3/4"},
	)
	res := Factorize(a)
	if res.Rank != 1 {
		t.Fatalf("rank = %d, want 1 after exact cancellation", res.Rank)
	}
	if res.U.NNZ() != 2 {
		t.Fatalf("U has %d nonzeros, want 2 (cancelled row must vanish)", res.U.NNZ())
	}
	checkFactorization(t, a, res)
}

// TestFillInCountsNonzeros checks that fill-in is created where the permuted
// input is zero, counted per nonzero entry, and that entries which later
// cancel are not left behind.
func TestFillInCountsNonzeros(t *testing.T) {
	// (1,1) and (2,2) are zero in A but fill in during elimination.
	a := mustMat(t,
		[]string{"1", "1", "1"},
		[]string{"1", "0", "1"},
		[]string{"1", "1", "0"},
	)
	res := Factorize(a)
	checkFactorization(t, a, res)
	if res.FillIn != 2 {
		t.Fatalf("FillIn = %d, want 2", res.FillIn)
	}
	// Pattern accounting: nnz(LU) = nnz(PAQ) + fill - cancelled, where
	// cancelled entries are PAQ nonzeros eliminated to exact zero.
	paq := Permute(a, res.P, res.Q)
	cancelled := 0
	for i, row := range paq.rows {
		for j := range row {
			inL := i > j && res.L.at(i, j) != nil
			inU := i <= j && res.U.at(i, j) != nil
			if !inL && !inU {
				cancelled++
			}
		}
	}
	// (1,2) and (2,1) of A cancel to exact zero during elimination.
	if cancelled != 2 {
		t.Fatalf("cancelled = %d, want 2", cancelled)
	}
	if got := res.L.NNZ() - res.L.Rows + res.U.NNZ(); got != paq.NNZ()+res.FillIn-cancelled {
		t.Fatalf("nnz(L)+nnz(U) = %d, want nnz(PAQ)+fill-cancelled = %d", got, paq.NNZ()+res.FillIn-cancelled)
	}
}

func TestSetDeletesZeros(t *testing.T) {
	m := NewMatrix(2, 2)
	m.Set(0, 0, big.NewRat(1, 2))
	m.Set(0, 0, big.NewRat(0, 1))
	if m.NNZ() != 0 {
		t.Fatalf("NNZ = %d after zeroing, want 0", m.NNZ())
	}
	if v := m.Get(0, 0); v.Sign() != 0 {
		t.Fatalf("Get after delete = %v, want 0", v)
	}
}

func TestMultiplyIndependent(t *testing.T) {
	a := mustMat(t,
		[]string{"1", "0", "2"},
		[]string{"0", "3", "0"},
	)
	b := mustMat(t,
		[]string{"1", "0"},
		[]string{"0", "0"},
		[]string{"1/2", "4"},
	)
	got := Multiply(a, b)
	want := mustMat(t,
		[]string{"2", "8"},
		[]string{"0", "0"},
	)
	if !Equal(got, want) {
		t.Fatalf("product:\n%s\nwant:\n%s", got, want)
	}
	// Exact cancellation in the dot product leaves no stored zero.
	c := mustMat(t, []string{"1", "1"})
	d := mustMat(t, []string{"1"}, []string{"-1"})
	if got := Multiply(c, d); got.NNZ() != 0 {
		t.Fatalf("cancelling product has %d nonzeros, want 0", got.NNZ())
	}
}
