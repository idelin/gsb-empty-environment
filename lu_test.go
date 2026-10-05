package sparselu

import (
	"math/big"
	"math/rand"
	"testing"
)

// mat builds a Matrix from rows of rational literals ("3/2", "-4", "0").
func mat(t *testing.T, rows ...[]string) *Matrix {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("mat: no rows")
	}
	m := New(len(rows), len(rows[0]))
	for i, row := range rows {
		if len(row) != m.Cols {
			t.Fatalf("mat: ragged row %d", i)
		}
		for j, s := range row {
			v, ok := new(big.Rat).SetString(s)
			if !ok {
				t.Fatalf("mat: bad rational %q", s)
			}
			m.Set(i, j, v)
		}
	}
	return m
}

func mustFactorize(t *testing.T, a *Matrix) *Factorization {
	t.Helper()
	f, err := Factorize(a, -1)
	if err != nil {
		t.Fatalf("Factorize: %v", err)
	}
	if !Verify(a, f) {
		t.Fatalf("PAQ != LU\nA:\n%sL:\n%sU:\n%sP=%v Q=%v", a, f.L, f.U, f.P, f.Q)
	}
	return f
}

func TestBasicSquare(t *testing.T) {
	a := mat(t,
		[]string{"2", "1", "1"},
		[]string{"4", "3", "3"},
		[]string{"8", "7", "9"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 3 {
		t.Fatalf("rank = %d, want 3", f.Rank)
	}
	for i := 0; i < 3; i++ {
		if f.L.At(i, i).Cmp(big.NewRat(1, 1)) != 0 {
			t.Fatalf("L diagonal not unit at %d", i)
		}
		for j := 0; j < i; j++ {
			if f.U.At(i, j).Sign() != 0 {
				t.Fatalf("U not upper at (%d,%d)", i, j)
			}
		}
	}
}

// TestColumnSwapOnly uses a matrix whose first column is entirely zero:
// no row swap can produce a nonzero pivot, only a column swap can.
func TestColumnSwapOnly(t *testing.T) {
	a := mat(t,
		[]string{"0", "1"},
		[]string{"0", "2"},
	)
	f := mustFactorize(t, a)
	if f.Q[0] != 1 || f.Q[1] != 0 {
		t.Fatalf("expected column swap Q=[1 0], got %v", f.Q)
	}
	if f.Rank != 1 {
		t.Fatalf("rank = %d, want 1", f.Rank)
	}
	// PAQ = [[1 0] [2 0]] = L U with L=[[1 0] [2 1]], U=[[1 0] [0 0]].
	want := mat(t, []string{"1", "0"}, []string{"2", "0"})
	if !Mul(f.L, f.U).Equal(want) {
		t.Fatalf("unexpected LU product:\n%s", Mul(f.L, f.U))
	}
}

func TestRowSwap(t *testing.T) {
	a := mat(t,
		[]string{"0", "0", "1"},
		[]string{"0", "0", "2"},
		[]string{"0", "0", "3"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 1 {
		t.Fatalf("rank = %d, want 1", f.Rank)
	}
}

func TestRectangularWide(t *testing.T) {
	a := mat(t,
		[]string{"1", "2", "3"},
		[]string{"4", "5", "6"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 2 {
		t.Fatalf("rank = %d, want 2", f.Rank)
	}
	if f.L.Rows != 2 || f.L.Cols != 2 || f.U.Rows != 2 || f.U.Cols != 3 {
		t.Fatalf("bad shapes: L=%dx%d U=%dx%d", f.L.Rows, f.L.Cols, f.U.Rows, f.U.Cols)
	}
}

func TestRectangularTall(t *testing.T) {
	a := mat(t,
		[]string{"1", "2"},
		[]string{"3", "4"},
		[]string{"5", "6"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 2 {
		t.Fatalf("rank = %d, want 2", f.Rank)
	}
	if f.L.Rows != 3 || f.L.Cols != 3 || f.U.Rows != 3 || f.U.Cols != 2 {
		t.Fatalf("bad shapes: L=%dx%d U=%dx%d", f.L.Rows, f.L.Cols, f.U.Rows, f.U.Cols)
	}
}

func TestRectangularRankDeficient(t *testing.T) {
	a := mat(t,
		[]string{"1", "2", "3"},
		[]string{"2", "4", "6"},
		[]string{"0", "1", "1"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 2 {
		t.Fatalf("rank = %d, want 2", f.Rank)
	}
}

func TestRankReported(t *testing.T) {
	a := mat(t,
		[]string{"1", "2"},
		[]string{"2", "4"},
	)
	f := mustFactorize(t, a)
	if f.Rank != 1 {
		t.Fatalf("rank = %d, want 1", f.Rank)
	}
	if f.U.NNZ() != 2 {
		t.Fatalf("U nnz = %d, want 2 (second row fully cancelled)", f.U.NNZ())
	}
}

// TestNoResidualZeros checks that entries cancelled to zero during
// elimination are removed, not stored: NNZ counts only true nonzeros.
func TestNoResidualZeros(t *testing.T) {
	a := mat(t,
		[]string{"1", "1", "1"},
		[]string{"1", "1", "2"},
		[]string{"1", "2", "1"},
	)
	f := mustFactorize(t, a)
	// U = [[1 1 1] [0 1 0] [0 0 1]] after elimination; exactly 5 nonzeros.
	if f.U.NNZ() != 5 {
		t.Fatalf("U nnz = %d, want 5; U:\n%s", f.U.NNZ(), f.U)
	}
}

// TestBudgetExhaustedResume drives the factorizer with a zero fill-in
// budget on a matrix that needs exactly one fill-in, then resumes.
func TestBudgetExhaustedResume(t *testing.T) {
	mk := func() *Matrix {
		return mat(t,
			[]string{"1", "1", "0"},
			[]string{"1", "0", "1"},
			[]string{"0", "1", "1"},
		)
	}
	a := mk()

	fz := NewFactorizer(a)
	if err := fz.Run(0); err != ErrBudgetExhausted {
		t.Fatalf("Run(0) = %v, want ErrBudgetExhausted", err)
	}
	if fz.Done() {
		t.Fatal("factorizer done despite exhausted budget")
	}
	// State is stable: rerunning with the same budget fails identically.
	if err := fz.Run(0); err != ErrBudgetExhausted {
		t.Fatalf("second Run(0) = %v, want ErrBudgetExhausted", err)
	}
	// Resume with exactly the required budget.
	if err := fz.Run(1); err != nil {
		t.Fatalf("Run(1) after resume: %v", err)
	}
	if !fz.Done() {
		t.Fatal("not done after resume")
	}
	f := fz.Result()
	if f == nil {
		t.Fatal("Result returned nil after completion")
	}
	if f.FillIn != 1 {
		t.Fatalf("fill-in = %d, want 1", f.FillIn)
	}
	if !Verify(a, f) {
		t.Fatalf("resumed factorization invalid\nA:\n%sL:\n%sU:\n%s", a, f.L, f.U)
	}

	// The resumed result must equal the one-shot result exactly.
	oneShot, err := Factorize(mk(), -1)
	if err != nil {
		t.Fatal(err)
	}
	if !f.L.Equal(oneShot.L) || !f.U.Equal(oneShot.U) {
		t.Fatal("resumed factorization differs from one-shot")
	}
}

// TestBudgetOneShotTooSmall confirms the convenience wrapper surfaces the
// budget error instead of a partial result.
func TestBudgetOneShotTooSmall(t *testing.T) {
	a := mat(t,
		[]string{"1", "1", "0"},
		[]string{"1", "0", "1"},
		[]string{"0", "1", "1"},
	)
	if _, err := Factorize(a, 0); err != ErrBudgetExhausted {
		t.Fatalf("Factorize budget 0 = %v, want ErrBudgetExhausted", err)
	}
}

func TestResultBeforeDoneIsNil(t *testing.T) {
	a := mat(t,
		[]string{"1", "1", "0"},
		[]string{"1", "0", "1"},
		[]string{"0", "1", "1"},
	)
	fz := NewFactorizer(a)
	if err := fz.Run(0); err != ErrBudgetExhausted {
		t.Fatalf("Run(0) = %v", err)
	}
	if fz.Result() != nil {
		t.Fatal("Result before completion should be nil")
	}
}

// TestRandomized factors random small rational matrices (including
// rectangular and rank-deficient ones) and verifies PAQ = LU. A random
// subset is factorized under a randomly stepped budget, resuming until
// completion, to exercise recoverability.
func TestRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		m := 1 + rng.Intn(4)
		n := 1 + rng.Intn(4)
		a := New(m, n)
		// Bias toward sparsity and repeated values to force cancellations.
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				if rng.Intn(2) == 0 {
					continue
				}
				num := int64(rng.Intn(5) - 2)
				den := int64(1 + rng.Intn(3))
				a.Set(i, j, big.NewRat(num, den))
			}
		}
		fz := NewFactorizer(a)
		budget := 0
		for !fz.Done() {
			var err error
			if trial%3 == 0 {
				err = fz.Run(budget)
				budget += rng.Intn(3)
			} else {
				err = fz.Run(-1)
			}
			if err != nil && err != ErrBudgetExhausted {
				t.Fatalf("trial %d: unexpected error %v", trial, err)
			}
		}
		f := fz.Result()
		if !Verify(a, f) {
			t.Fatalf("trial %d: PAQ != LU\nA:\n%sL:\n%sU:\n%sP=%v Q=%v",
				trial, a, f.L, f.U, f.P, f.Q)
		}
		if f.Rank > m || f.Rank > n {
			t.Fatalf("trial %d: rank %d exceeds dimensions %dx%d", trial, f.Rank, m, n)
		}
	}
}

func TestZeroMatrix(t *testing.T) {
	a := New(3, 2)
	f := mustFactorize(t, a)
	if f.Rank != 0 {
		t.Fatalf("rank = %d, want 0", f.Rank)
	}
	if f.U.NNZ() != 0 {
		t.Fatalf("U not empty for zero matrix")
	}
}

func TestPermutationsAreValid(t *testing.T) {
	a := mat(t,
		[]string{"0", "3", "1/2"},
		[]string{"-2", "0", "0"},
		[]string{"5", "1", "1"},
	)
	f := mustFactorize(t, a)
	if !isPermutation(f.P) || !isPermutation(f.Q) {
		t.Fatalf("invalid permutations P=%v Q=%v", f.P, f.Q)
	}
}
