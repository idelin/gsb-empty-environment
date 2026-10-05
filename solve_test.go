package main

import (
	"math/big"
	"math/rand"
	"testing"
)

func mkProblem(A [][]int64, b, lo, hi []int64) *Problem {
	p := &Problem{m: len(b), n: len(lo)}
	for _, row := range A {
		p.A = append(p.A, intVec(row...))
	}
	p.b = intVec(b...)
	p.lo = intVec(lo...)
	p.hi = intVec(hi...)
	return p
}

func vecEq(a, b []*big.Int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(b[i]) != 0 {
			return false
		}
	}
	return true
}

func feasible(p *Problem, x []*big.Int) bool {
	if !inBox(x, p.lo, p.hi) {
		return false
	}
	Ax := matVec(p.A, x)
	return vecEq(Ax, p.b)
}

func TestUnique(t *testing.T) {
	p := mkProblem([][]int64{{1, 1}, {1, -1}}, []int64{5, 1}, []int64{0, 0}, []int64{10, 10})
	res := Solve(p)
	if res.Status != "unique" {
		t.Fatalf("status = %s, want unique", res.Status)
	}
	if !vecEq(res.Signals[0], intVec(3, 2)) {
		t.Fatalf("signal = %v, want [3 2]", res.Signals[0])
	}
}

func TestMultiple(t *testing.T) {
	p := mkProblem([][]int64{{1, 1}}, []int64{3}, []int64{0, 0}, []int64{10, 10})
	res := Solve(p)
	if res.Status != "multiple" {
		t.Fatalf("status = %s, want multiple", res.Status)
	}
	if len(res.Signals) != 2 {
		t.Fatalf("got %d signals, want 2", len(res.Signals))
	}
	if vecEq(res.Signals[0], res.Signals[1]) {
		t.Fatal("the two signals are identical")
	}
	for _, s := range res.Signals {
		if !feasible(p, s) {
			t.Fatalf("signal %v is not feasible", s)
		}
	}
}

// Rank deficiency does not imply multiple integer solutions: here the
// matrix has rank 1 < 2, yet the bounds pin down a single feasible signal.
func TestRankDeficientButUnique(t *testing.T) {
	p := mkProblem([][]int64{{1, 1}}, []int64{4}, []int64{0, 0}, []int64{2, 2})
	res := Solve(p)
	if res.Status != "unique" {
		t.Fatalf("status = %s, want unique", res.Status)
	}
	if !vecEq(res.Signals[0], intVec(2, 2)) {
		t.Fatalf("signal = %v, want [2 2]", res.Signals[0])
	}
}

// Full rational rank does not imply integer feasibility.
func TestFullRankButNoIntegerSolution(t *testing.T) {
	p := mkProblem([][]int64{{2}}, []int64{3}, []int64{0}, []int64{10})
	res := Solve(p)
	if res.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
	if len(res.Conflict.Observations) != 1 || res.Conflict.Observations[0] != 0 {
		t.Fatalf("conflict observations = %v, want [0]", res.Conflict.Observations)
	}
}

func TestConflictingObservations(t *testing.T) {
	p := mkProblem([][]int64{{1, 1}, {2, 2}}, []int64{3, 7}, []int64{0, 0}, []int64{10, 10})
	res := Solve(p)
	if res.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
	cf := res.Conflict
	if len(cf.Coefficients) != 2 {
		t.Fatalf("certificate coefficients = %v, want 2 entries", cf.Coefficients)
	}
	// Verify the certificate: coeffs^T * A == 0 but coeffs^T * b != 0.
	for j := 0; j < p.n; j++ {
		dot := new(big.Int)
		for i := 0; i < p.m; i++ {
			dot.Add(dot, new(big.Int).Mul(cf.Coefficients[i], p.A[i][j]))
		}
		if dot.Sign() != 0 {
			t.Fatalf("certificate column %d dot = %s, want 0", j, dot)
		}
	}
	dot := new(big.Int)
	for i := 0; i < p.m; i++ {
		dot.Add(dot, new(big.Int).Mul(cf.Coefficients[i], p.b[i]))
	}
	if dot.Sign() == 0 {
		t.Fatal("certificate rhs dot = 0, want nonzero")
	}
}

func TestBoundsInfeasible(t *testing.T) {
	p := mkProblem([][]int64{{1}}, []int64{5}, []int64{0}, []int64{3})
	res := Solve(p)
	if res.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
	if len(res.Conflict.Observations) != 1 || res.Conflict.Observations[0] != 0 {
		t.Fatalf("conflict = %v, want [0]", res.Conflict.Observations)
	}
}

func TestEmptyBox(t *testing.T) {
	p := mkProblem([][]int64{{1}}, []int64{1}, []int64{5}, []int64{3})
	res := Solve(p)
	if res.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
}

func TestNoObservations(t *testing.T) {
	p := mkProblem(nil, nil, []int64{0, 0}, []int64{1, 1})
	if res := Solve(p); res.Status != "multiple" {
		t.Fatalf("status = %s, want multiple", res.Status)
	}
	p2 := mkProblem(nil, nil, []int64{2, 3}, []int64{2, 3})
	res := Solve(p2)
	if res.Status != "unique" || !vecEq(res.Signals[0], intVec(2, 3)) {
		t.Fatalf("got %+v, want unique [2 3]", res)
	}
}

func TestDuplicateRows(t *testing.T) {
	// Same equation observed twice: consistent, unique.
	p := mkProblem([][]int64{{1, 2}, {2, 4}, {1, 0}}, []int64{5, 10, 1}, []int64{0, 0}, []int64{9, 9})
	res := Solve(p)
	if res.Status != "unique" || !vecEq(res.Signals[0], intVec(1, 2)) {
		t.Fatalf("got %+v, want unique [1 2]", res)
	}
}

func TestZeroObservationRow(t *testing.T) {
	p := mkProblem([][]int64{{0, 0}, {1, 0}}, []int64{0, 4}, []int64{0, 0}, []int64{5, 5})
	res := Solve(p)
	if res.Status != "multiple" {
		t.Fatalf("status = %s, want multiple", res.Status)
	}
	p2 := mkProblem([][]int64{{0, 0}}, []int64{3}, []int64{0, 0}, []int64{5, 5})
	if res := Solve(p2); res.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", res.Status)
	}
}

func TestNegativeBoundsAndValues(t *testing.T) {
	p := mkProblem([][]int64{{3, -2}, {1, 1}}, []int64{-5, 5}, []int64{-5, -5}, []int64{5, 5})
	res := Solve(p)
	if res.Status != "unique" {
		t.Fatalf("status = %s, want unique", res.Status)
	}
	if !feasible(p, res.Signals[0]) {
		t.Fatalf("signal %v infeasible", res.Signals[0])
	}
	if !vecEq(res.Signals[0], intVec(1, 4)) {
		t.Fatalf("signal = %v, want [1 4]", res.Signals[0])
	}
}

func TestTwelveDimensional(t *testing.T) {
	// Identity observations pin down all 12 components.
	A := make([][]int64, 12)
	b := make([]int64, 12)
	lo := make([]int64, 12)
	hi := make([]int64, 12)
	for i := 0; i < 12; i++ {
		A[i] = make([]int64, 12)
		A[i][i] = 1
		b[i] = int64(i * i)
		lo[i] = -200
		hi[i] = 200
	}
	res := Solve(mkProblem(A, b, lo, hi))
	if res.Status != "unique" {
		t.Fatalf("status = %s, want unique", res.Status)
	}
	// Drop half the observations: no longer unique.
	res2 := Solve(mkProblem(A[:6], b[:6], lo, hi))
	if res2.Status != "multiple" {
		t.Fatalf("status = %s, want multiple", res2.Status)
	}
}

// bruteForce enumerates every integer point of the box (only for tiny tests).
func bruteForce(p *Problem) [][]*big.Int {
	var out [][]*big.Int
	x := cloneVec(p.lo)
	for {
		if feasible(p, x) {
			out = append(out, cloneVec(x))
		}
		i := 0
		for ; i < p.n; i++ {
			if x[i].Cmp(p.hi[i]) < 0 {
				x[i].Add(x[i], big.NewInt(1))
				break
			}
			x[i].Set(p.lo[i])
		}
		if i == p.n {
			return out
		}
	}
}

// TestAgainstBruteForce cross-checks Solve against exhaustive enumeration
// on random small instances, including rank-deficient and inconsistent ones.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(4)
		m := rng.Intn(5)
		A := make([][]int64, m)
		b := make([]int64, m)
		lo := make([]int64, n)
		hi := make([]int64, n)
		for i := range A {
			A[i] = make([]int64, n)
			for j := range A[i] {
				A[i][j] = int64(rng.Intn(7) - 3)
			}
			b[i] = int64(rng.Intn(11) - 5)
		}
		for j := 0; j < n; j++ {
			lo[j] = int64(rng.Intn(4) - 3)
			hi[j] = lo[j] + int64(rng.Intn(4))
		}
		p := mkProblem(A, b, lo, hi)
		res := Solve(p)
		all := bruteForce(p)

		switch {
		case len(all) == 0:
			if res.Status != "infeasible" {
				t.Fatalf("trial %d: status = %s, want infeasible (A=%v b=%v lo=%v hi=%v)", trial, res.Status, A, b, lo, hi)
			}
			// The reported conflict subset must itself be infeasible.
			sub := subProblem(p, res.Conflict.Observations)
			if got := bruteForce(sub); len(got) != 0 {
				t.Fatalf("trial %d: conflict subset is feasible", trial)
			}
		case len(all) == 1:
			if res.Status != "unique" {
				t.Fatalf("trial %d: status = %s, want unique (A=%v b=%v lo=%v hi=%v)", trial, res.Status, A, b, lo, hi)
			}
			if !vecEq(res.Signals[0], all[0]) {
				t.Fatalf("trial %d: signal = %v, want %v", trial, res.Signals[0], all[0])
			}
		default:
			if res.Status != "multiple" {
				t.Fatalf("trial %d: status = %s, want multiple (A=%v b=%v lo=%v hi=%v)", trial, res.Status, A, b, lo, hi)
			}
			if vecEq(res.Signals[0], res.Signals[1]) {
				t.Fatalf("trial %d: identical signals", trial)
			}
			for _, s := range res.Signals {
				if !feasible(p, s) {
					t.Fatalf("trial %d: signal %v infeasible", trial, s)
				}
			}
		}
	}
}
