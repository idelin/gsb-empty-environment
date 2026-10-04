package ils

import (
	"math/big"
	"math/rand"
	"testing"
)

func ints(vs ...int64) []*big.Int {
	out := make([]*big.Int, len(vs))
	for i, v := range vs {
		out[i] = big.NewInt(v)
	}
	return out
}

func solveOK(t *testing.T, A *Matrix, b []*big.Int) *Solution {
	t.Helper()
	sol, cert, err := Solve(A, b)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	if sol == nil {
		t.Fatalf("expected solution, got certificate y=%v/%v", cert.Num, cert.Den)
	}
	if err := VerifySolution(A, b, sol); err != nil {
		t.Fatalf("VerifySolution: %v", err)
	}
	return sol
}

func solveUnsat(t *testing.T, A *Matrix, b []*big.Int) {
	t.Helper()
	sol, cert, err := Solve(A, b)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	if sol != nil {
		t.Fatalf("expected infeasibility certificate, got solution x0=%v", sol.X0)
	}
	if err := VerifyUnsat(A, b, cert); err != nil {
		t.Fatalf("VerifyUnsat: %v", err)
	}
}

func TestBasicSolvable(t *testing.T) {
	// x + y = 3, x - y = 1  =>  x = 2, y = 1
	A := MatrixFromInts([][]int64{{1, 1}, {1, -1}})
	sol := solveOK(t, A, ints(3, 1))
	if sol.X0[0].Cmp(big.NewInt(2)) != 0 || sol.X0[1].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("unexpected x0 = %v", sol.X0)
	}
	if len(sol.Kernel) != 0 {
		t.Fatalf("expected trivial kernel, got %v", sol.Kernel)
	}
}

func TestRankDeficientKernel(t *testing.T) {
	// x + y = 3 (duplicated); kernel = span{(1,-1)}, dim 1.
	A := MatrixFromInts([][]int64{{1, 1}, {2, 2}, {-1, -1}})
	sol := solveOK(t, A, ints(3, 6, -3))
	if len(sol.Kernel) != 1 {
		t.Fatalf("kernel dim = %d, want 1", len(sol.Kernel))
	}
}

func TestUnsatDivisibility(t *testing.T) {
	// 2x = 1 has no integer solution (a rational one is not enough).
	solveUnsat(t, MatrixFromInts([][]int64{{2}}), ints(1))
	// 2x + 4y = 3
	solveUnsat(t, MatrixFromInts([][]int64{{2, 4}}), ints(3))
	// 6x + 10y = 7
	solveUnsat(t, MatrixFromInts([][]int64{{6, 10}}), ints(7))
}

func TestUnsatConsistency(t *testing.T) {
	// Dependent rows with contradictory right-hand sides.
	solveUnsat(t, MatrixFromInts([][]int64{{1, 1}, {2, 2}}), ints(3, 7))
	solveUnsat(t, MatrixFromInts([][]int64{{1, 2}, {2, 4}, {3, 6}}), ints(1, 2, 4))
	// Zero row with nonzero rhs.
	solveUnsat(t, MatrixFromInts([][]int64{{1, 1}, {0, 0}}), ints(2, 5))
}

func TestZeroRowsAndCols(t *testing.T) {
	// Zero row with zero rhs: solvable.
	A := MatrixFromInts([][]int64{{1, 1}, {0, 0}, {1, -1}})
	sol := solveOK(t, A, ints(3, 0, 1))
	if len(sol.Kernel) != 0 {
		t.Fatalf("expected trivial kernel")
	}
	// Zero matrix, zero b: everything is a solution, kernel = Z^n.
	Z := NewMatrix(3, 2)
	sol = solveOK(t, Z, ints(0, 0, 0))
	if len(sol.Kernel) != 2 {
		t.Fatalf("kernel dim = %d, want 2", len(sol.Kernel))
	}
	// Zero matrix, nonzero b: infeasible.
	solveUnsat(t, NewMatrix(2, 3), ints(0, 1))
	// No variables (n = 0): solvable iff b = 0.
	A0 := NewMatrix(2, 0)
	sol = solveOK(t, A0, ints(0, 0))
	if len(sol.X0) != 0 || len(sol.Kernel) != 0 {
		t.Fatalf("bad empty solution: %+v", sol)
	}
	solveUnsat(t, NewMatrix(1, 0), ints(4))
	// No equations (m = 0): x0 = 0, kernel = Z^n.
	E := NewMatrix(0, 3)
	sol = solveOK(t, E, nil)
	if len(sol.Kernel) != 3 {
		t.Fatalf("kernel dim = %d, want 3", len(sol.Kernel))
	}
}

func TestVerifierRejectsBadClaims(t *testing.T) {
	A := MatrixFromInts([][]int64{{1, 1}})
	b := ints(3)
	// Wrong particular solution.
	if err := VerifySolution(A, b, &Solution{X0: ints(1, 1), Kernel: [][]*big.Int{ints(1, -1)}}); err == nil {
		t.Fatal("accepted wrong X0")
	}
	// Kernel vector not in the kernel.
	if err := VerifySolution(A, b, &Solution{X0: ints(3, 0), Kernel: [][]*big.Int{ints(1, 1)}}); err == nil {
		t.Fatal("accepted non-kernel vector")
	}
	// Proper sublattice of the kernel: (2,-2) only generates the even part.
	if err := VerifySolution(A, b, &Solution{X0: ints(3, 0), Kernel: [][]*big.Int{ints(2, -2)}}); err == nil {
		t.Fatal("accepted incomplete (non-saturated) kernel lattice")
	}
	// Too few / too many basis vectors.
	if err := VerifySolution(A, b, &Solution{X0: ints(3, 0)}); err == nil {
		t.Fatal("accepted missing kernel basis")
	}
	if err := VerifySolution(A, b, &Solution{X0: ints(3, 0), Kernel: [][]*big.Int{ints(1, -1), ints(2, -2)}}); err == nil {
		t.Fatal("accepted too many kernel vectors")
	}
	// Bogus infeasibility certificate for a solvable system.
	if err := VerifyUnsat(A, b, &Certificate{Num: ints(1), Den: big.NewInt(2)}); err == nil {
		t.Fatal("accepted certificate with integral y*b")
	}
}

// enumSolutions lists all x in [-bound, bound]^n with A x = b.
func enumSolutions(A *Matrix, b []*big.Int, bound int64) [][]*big.Int {
	var out [][]*big.Int
	n := A.Cols
	x := make([]*big.Int, n)
	for j := range x {
		x[j] = new(big.Int)
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			Ax := A.MulVec(x)
			for i := range Ax {
				if Ax[i].Cmp(b[i]) != 0 {
					return
				}
			}
			cp := make([]*big.Int, n)
			for j := range cp {
				cp[j] = new(big.Int).Set(x[j])
			}
			out = append(out, cp)
			return
		}
		for v := -bound; v <= bound; v++ {
			x[k].SetInt64(v)
			rec(k + 1)
		}
	}
	rec(0)
	return out
}

// inLattice reports whether v is an integer combination of the rows of K,
// by solving K^T w = v over the integers with Solve itself.
func inLattice(K [][]*big.Int, v []*big.Int) bool {
	if len(K) == 0 {
		for _, c := range v {
			if c.Sign() != 0 {
				return false
			}
		}
		return true
	}
	KT := NewMatrix(len(v), len(K))
	for i, k := range K {
		for j := 0; j < len(v); j++ {
			KT.Set(j, i, k[j])
		}
	}
	sol, _, err := Solve(KT, v)
	return err == nil && sol != nil
}

func TestRandomSmallAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		m := rng.Intn(5)     // 0..4 equations
		n := 1 + rng.Intn(4) // 1..4 variables
		A := NewMatrix(m, n)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				A.Set(i, j, big.NewInt(int64(rng.Intn(7)-3)))
			}
		}
		b := make([]*big.Int, m)
		for i := range b {
			b[i] = big.NewInt(int64(rng.Intn(13) - 6))
		}

		sol, cert, err := Solve(A, b)
		if err != nil {
			t.Fatalf("trial %d: Solve error: %v", trial, err)
		}
		enum := enumSolutions(A, b, 4)

		if sol == nil {
			if err := VerifyUnsat(A, b, cert); err != nil {
				t.Fatalf("trial %d: bad certificate: %v", trial, err)
			}
			// Enumeration is conclusive here: any integer solution the
			// solver missed would have to lie outside the box as well,
			// but a *small* solution refutes UNSAT directly.
			if len(enum) != 0 {
				t.Fatalf("trial %d: solver says UNSAT but enumeration found %d solutions", trial, len(enum))
			}
			continue
		}
		if err := VerifySolution(A, b, sol); err != nil {
			t.Fatalf("trial %d: VerifySolution: %v", trial, err)
		}
		// Note: a solvable system need not have a solution inside the
		// enumeration box (the smallest solution can be large), so SAT
		// with an empty enumeration is fine; VerifySolution above is the
		// authoritative check in that direction.
		// Every enumerated solution must be X0 plus a lattice combination.
		diff := make([]*big.Int, n)
		for _, x := range enum {
			for j := 0; j < n; j++ {
				diff[j] = new(big.Int).Sub(x[j], sol.X0[j])
			}
			if !inLattice(sol.Kernel, diff) {
				t.Fatalf("trial %d: enumerated solution %v not in X0 + lattice", trial, x)
			}
		}
		// Independent completeness cross-check: gcd of maximal minors of
		// the kernel basis matrix must be 1 (saturated lattice).
		if s := len(sol.Kernel); s > 0 {
			if g := maximalMinorGCD(sol.Kernel, n); g.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("trial %d: minor gcd = %s, lattice not saturated", trial, g)
			}
		}
	}
}

func TestBigCoefficients(t *testing.T) {
	// 60-bit random coefficients: products and elimination intermediates
	// far exceed int64, so this exercises the big.Int arithmetic.
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		m := 1 + rng.Intn(5)
		n := 1 + rng.Intn(5)
		A := NewMatrix(m, n)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				v := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 60))
				if rng.Intn(2) == 0 {
					v.Neg(v)
				}
				A.Set(i, j, v)
			}
		}
		// Half the time build b from a known integer point, half random.
		b := make([]*big.Int, m)
		if trial%2 == 0 {
			x := make([]*big.Int, n)
			for j := range x {
				x[j] = new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 40))
			}
			Ax := A.MulVec(x)
			copy(b, Ax)
		} else {
			for i := range b {
				b[i] = new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 60))
			}
		}
		sol, cert, err := Solve(A, b)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if sol != nil {
			if err := VerifySolution(A, b, sol); err != nil {
				t.Fatalf("trial %d: VerifySolution: %v", trial, err)
			}
		} else if err := VerifyUnsat(A, b, cert); err != nil {
			t.Fatalf("trial %d: VerifyUnsat: %v", trial, err)
		}
	}
}

func TestWideAndTall(t *testing.T) {
	// Wide: 2 equations, 6 unknowns -> kernel dim 4.
	A := MatrixFromInts([][]int64{{1, 2, 3, 4, 5, 6}, {6, 5, 4, 3, 2, 1}})
	sol := solveOK(t, A, ints(21, 21))
	if len(sol.Kernel) != 4 {
		t.Fatalf("kernel dim = %d, want 4", len(sol.Kernel))
	}
	// Tall: 6 equations, 2 unknowns, consistent.
	T := MatrixFromInts([][]int64{{1, 0}, {0, 1}, {1, 1}, {2, 1}, {1, 2}, {3, 3}})
	sol = solveOK(t, T, ints(2, 3, 5, 7, 8, 15))
	if sol.X0[0].Cmp(big.NewInt(2)) != 0 || sol.X0[1].Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("unexpected x0 = %v", sol.X0)
	}
	// Tall and inconsistent.
	solveUnsat(t, T, ints(2, 3, 5, 7, 8, 16))
}
