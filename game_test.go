package matrixgame

import (
	"math/big"
	"math/rand"
	"testing"
)

func intMat(rows ...[]int64) Matrix {
	m := make(Matrix, len(rows))
	for i, r := range rows {
		m[i] = make([]*big.Rat, len(r))
		for j, v := range r {
			m[i][j] = big.NewRat(v, 1)
		}
	}
	return m
}

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func mustSolve(t *testing.T, a Matrix) *Solution {
	t.Helper()
	sol, err := Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := Verify(a, sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return sol
}

func expectValue(t *testing.T, sol *Solution, want string) {
	t.Helper()
	if sol.Value.Cmp(rat(t, want)) != 0 {
		t.Fatalf("value = %s, want %s", sol.Value.RatString(), want)
	}
}

func expectVec(t *testing.T, got []*big.Rat, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("vector length %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Cmp(rat(t, w)) != 0 {
			t.Fatalf("entry %d = %s, want %s", i, got[i].RatString(), w)
		}
	}
}

func TestPureSaddlePoint(t *testing.T) {
	a := intMat([]int64{2, 3}, []int64{1, 2})
	sol := mustSolve(t, a)
	expectValue(t, sol, "2")
	expectVec(t, sol.P, "1", "0")
	expectVec(t, sol.Q, "1", "0")
}

func TestSaddlePointNegative(t *testing.T) {
	a := intMat([]int64{-2, -3}, []int64{-1, -4})
	sol := mustSolve(t, a)
	expectValue(t, sol, "-3")
	expectVec(t, sol.P, "1", "0")
	expectVec(t, sol.Q, "0", "1")
}

func TestMatchingPennies(t *testing.T) {
	a := intMat([]int64{1, -1}, []int64{-1, 1})
	sol := mustSolve(t, a)
	expectValue(t, sol, "0")
	expectVec(t, sol.P, "1/2", "1/2")
	expectVec(t, sol.Q, "1/2", "1/2")
}

func TestRationalValue(t *testing.T) {
	a := intMat([]int64{1, 0}, []int64{0, 2})
	sol := mustSolve(t, a)
	expectValue(t, sol, "2/3")
	expectVec(t, sol.P, "2/3", "1/3")
	expectVec(t, sol.Q, "2/3", "1/3")
}

func TestMixedIntegerValue(t *testing.T) {
	a := intMat([]int64{0, 3}, []int64{4, 1})
	sol := mustSolve(t, a)
	expectValue(t, sol, "2")
	expectVec(t, sol.P, "1/2", "1/2")
	expectVec(t, sol.Q, "1/3", "2/3")
}

func TestAllNegativePayoffs(t *testing.T) {
	a := intMat([]int64{-5, -2}, []int64{-3, -4})
	sol := mustSolve(t, a)
	expectValue(t, sol, "-7/2")
	expectVec(t, sol.P, "1/4", "3/4")
	expectVec(t, sol.Q, "1/2", "1/2")
}

func TestDuplicateRowsAndColumns(t *testing.T) {
	// Row 1 duplicates row 0; column 2 duplicates column 0.
	a := intMat(
		[]int64{1, -1, 1},
		[]int64{1, -1, 1},
		[]int64{-1, 1, -1},
	)
	sol := mustSolve(t, a)
	expectValue(t, sol, "0")
}

func TestMultipleOptimalStrategies(t *testing.T) {
	// Row 0 is a pure optimum; any column mix with q0 >= 1/2 is optimal.
	a := intMat([]int64{2, 2}, []int64{1, 3})
	sol := mustSolve(t, a)
	expectValue(t, sol, "2")
	expectVec(t, sol.P, "1", "0")
	// The solver may return any optimal q; Verify already certified it.
}

func TestDominatedStrategies(t *testing.T) {
	// Row 2 and column 2 are strictly dominated copies.
	a := intMat(
		[]int64{1, 0, 1},
		[]int64{0, 2, 0},
		[]int64{0, 0, 0},
	)
	sol := mustSolve(t, a)
	expectValue(t, sol, "2/3")
}

func TestRectangularMoreRows(t *testing.T) {
	a := intMat(
		[]int64{3, 0},
		[]int64{0, 3},
		[]int64{1, 1},
	)
	sol := mustSolve(t, a)
	expectValue(t, sol, "3/2")
	expectVec(t, sol.Q, "1/2", "1/2")
}

func TestRectangularMoreColumns(t *testing.T) {
	a := intMat(
		[]int64{4, 0, 2},
		[]int64{0, 4, 2},
	)
	sol := mustSolve(t, a)
	expectValue(t, sol, "2")
	expectVec(t, sol.P, "1/2", "1/2")
}

func TestFractionalEntries(t *testing.T) {
	a := Matrix{
		{rat(t, "1/2"), rat(t, "-1/3")},
		{rat(t, "-1/4"), rat(t, "2/5")},
	}
	sol := mustSolve(t, a)
	// Value of [[a,b],[c,d]] = (ad-bc)/(a+d-b-c).
	// ad-bc = 1/5 - 1/12 = 7/60; a+d-b-c = 1/2+2/5+1/3+1/4 = 47/30+... = 47/60... compute:
	// 1/2+2/5 = 9/10; 9/10+1/3 = 37/30; 37/30+1/4 = 74/60+15/60 = 89/60.
	expectValue(t, sol, "7/89")
}

func TestSingleCell(t *testing.T) {
	a := intMat([]int64{-7})
	sol := mustSolve(t, a)
	expectValue(t, sol, "-7")
	expectVec(t, sol.P, "1")
	expectVec(t, sol.Q, "1")
}

func TestVerifyRejectsWrongValue(t *testing.T) {
	a := intMat([]int64{1, -1}, []int64{-1, 1})
	sol := mustSolve(t, a)
	bad := *sol
	bad.Value = big.NewRat(1, 3)
	if err := Verify(a, &bad); err == nil {
		t.Fatal("Verify accepted a tampered value")
	}
}

func TestVerifyRejectsBadStrategy(t *testing.T) {
	a := intMat([]int64{1, -1}, []int64{-1, 1})
	sol := mustSolve(t, a)
	bad := *sol
	bad.P = []*big.Rat{big.NewRat(1, 1), big.NewRat(0, 1)}
	if err := Verify(a, &bad); err == nil {
		t.Fatal("Verify accepted a non-optimal row strategy")
	}
}

func TestVerifyRejectsNonProbability(t *testing.T) {
	a := intMat([]int64{1, -1}, []int64{-1, 1})
	sol := mustSolve(t, a)
	bad := *sol
	bad.Q = []*big.Rat{big.NewRat(3, 4), big.NewRat(3, 4)}
	if err := Verify(a, &bad); err == nil {
		t.Fatal("Verify accepted probabilities not summing to 1")
	}
}

func TestRandomGamesVerified(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		m := 1 + rng.Intn(4)
		n := 1 + rng.Intn(4)
		a := make(Matrix, m)
		for i := range a {
			a[i] = make([]*big.Rat, n)
			for j := range a[i] {
				a[i][j] = big.NewRat(int64(rng.Intn(21)-10), 1)
			}
		}
		sol, err := Solve(a)
		if err != nil {
			t.Fatalf("trial %d (%dx%d): Solve: %v", trial, m, n, err)
		}
		if err := Verify(a, sol); err != nil {
			t.Fatalf("trial %d (%dx%d): Verify: %v", trial, m, n, err)
		}
		// Both players mixing optimally must yield exactly the value.
		got := new(big.Rat)
		for i := 0; i < m; i++ {
			got.Add(got, new(big.Rat).Mul(sol.P[i], dot(a[i], sol.Q)))
		}
		if got.Cmp(sol.Value) != 0 {
			t.Fatalf("trial %d: p^T A q = %s, value = %s", trial, got.RatString(), sol.Value.RatString())
		}
	}
}
