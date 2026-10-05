package ilse

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func matFromInts(rows [][]int64) *Mat {
	m := NewMat(len(rows), len(rows[0]))
	for i, row := range rows {
		if len(row) != m.Cols {
			panic("ragged")
		}
		for j, v := range row {
			m.Data[i*m.Cols+j].SetInt64(v)
		}
	}
	return m
}

func vecFromInts(v ...int64) []*big.Int {
	out := make([]*big.Int, len(v))
	for i, x := range v {
		out[i] = big.NewInt(x)
	}
	return out
}

func zeroVec(n int) []*big.Int {
	out := make([]*big.Int, n)
	for i := range out {
		out[i] = new(big.Int)
	}
	return out
}

// checkSolved verifies a feasible outcome thoroughly.
func checkSolved(t *testing.T, A *Mat, b []*big.Int, sol *Solution) {
	t.Helper()
	if err := VerifySolution(A, b, sol); err != nil {
		t.Fatalf("VerifySolution failed: %v", err)
	}
	// Sample lattice points must satisfy the system and round-trip InLattice.
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 8; trial++ {
		z := make([]*big.Int, sol.B.Cols)
		for j := range z {
			z[j] = big.NewInt(int64(rng.Intn(11) - 5))
		}
		x := sol.LatticePoint(z)
		if got := A.MulVec(x); !vecEqual(got, b) {
			t.Fatalf("lattice point %v does not solve the system", x)
		}
		if !sol.InLattice(x) {
			t.Fatalf("InLattice rejects lattice point %v", x)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	type tc struct {
		name     string
		A        *Mat
		b        []*big.Int
		feasible bool
		rank     int
	}
	cases := []tc{
		{"zero matrix, b=0", NewMat(2, 3), zeroVec(2), true, 0},
		{"zero matrix, b!=0", NewMat(2, 3), vecFromInts(0, 5), false, 0},
		{"1x1 divisible", matFromInts([][]int64{{2}}), vecFromInts(4), true, 1},
		{"1x1 not divisible", matFromInts([][]int64{{2}}), vecFromInts(3), false, 1},
		{"zero row consistent", matFromInts([][]int64{{1, 2}, {0, 0}}), vecFromInts(3, 0), true, 1},
		{"zero row inconsistent", matFromInts([][]int64{{1, 2}, {0, 0}}), vecFromInts(3, 1), false, 1},
		{"rank deficient feasible", matFromInts([][]int64{{1, 1}, {2, 2}}), vecFromInts(3, 6), true, 1},
		{"rank deficient infeasible", matFromInts([][]int64{{1, 1}, {2, 2}}), vecFromInts(3, 7), false, 1},
		{"wide underdetermined", matFromInts([][]int64{{2, 4, 1, 6}}), vecFromInts(8), true, 1},
		{"tall overdetermined", matFromInts([][]int64{{1, 0}, {0, 1}, {1, 1}, {2, 3}, {0, 0}}), vecFromInts(1, 2, 3, 8, 0), true, 2},
		{"tall inconsistent", matFromInts([][]int64{{1, 0}, {0, 1}, {1, 1}}), vecFromInts(1, 2, 5), false, 2},
		{"congruence obstruction", matFromInts([][]int64{{2, 0}, {0, 2}}), vecFromInts(1, 2), false, 2},
		{"homogeneous", matFromInts([][]int64{{1, -1, 0}, {0, 1, -1}}), zeroVec(2), true, 2},
		{"no columns feasible", NewMat(2, 0), zeroVec(2), true, 0},
		{"no columns infeasible", NewMat(2, 0), vecFromInts(0, 1), false, 0},
		{"no rows", NewMat(0, 3), zeroVec(0), true, 0},
		{"identity", Eye(3), vecFromInts(7, -2, 5), true, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sol, inf := Solve(c.A, c.b)
			if c.feasible {
				if sol == nil {
					t.Fatalf("expected feasible, got infeasible cert %v", inf.C)
				}
				if sol.Rank != c.rank {
					t.Fatalf("rank = %d, want %d", sol.Rank, c.rank)
				}
				checkSolved(t, c.A, c.b, sol)
			} else {
				if inf == nil {
					t.Fatalf("expected infeasible, got solution %v", sol.X0)
				}
				if err := VerifyInfeasible(c.A, c.b, inf); err != nil {
					t.Fatalf("bad infeasibility certificate: %v", err)
				}
			}
		})
	}
}

func TestKnownSolution(t *testing.T) {
	// x + 2y = 5, 3x - y = 1  =>  x = 1, y = 2.
	A := matFromInts([][]int64{{1, 2}, {3, -1}})
	b := vecFromInts(5, 1)
	sol, inf := Solve(A, b)
	if sol == nil {
		t.Fatalf("unexpected infeasible: %v", inf.C)
	}
	want := vecFromInts(1, 2)
	if !vecEqual(sol.X0, want) {
		t.Fatalf("X0 = %v, want %v", sol.X0, want)
	}
	if sol.B.Cols != 0 {
		t.Fatalf("expected trivial lattice, got %d basis vectors", sol.B.Cols)
	}
	if err := VerifySolution(A, b, sol); err != nil {
		t.Fatal(err)
	}
}

func randMat(rng *rand.Rand, m, n int, coef int64) *Mat {
	A := NewMat(m, n)
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			A.Data[i*n+j].SetInt64(rng.Int63n(2*coef+1) - coef)
		}
	}
	return A
}

func randVec(rng *rand.Rand, n int, coef int64) []*big.Int {
	v := make([]*big.Int, n)
	for i := range v {
		v[i] = big.NewInt(rng.Int63n(2*coef+1) - coef)
	}
	return v
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const bound = 3
	for trial := 0; trial < 300; trial++ {
		m := int(rng.Intn(5))      // 0..4 rows
		n := int(rng.Intn(6))      // 0..5 cols
		A := randMat(rng, m, n, 3) // small coefficients
		b := randVec(rng, m, 5)
		sol, inf := Solve(A, b)
		enum := Enumerate(A, b, bound, 0)
		if sol == nil {
			if err := VerifyInfeasible(A, b, inf); err != nil {
				t.Fatalf("trial %d: bad certificate: %v", trial, err)
			}
			if len(enum) != 0 {
				t.Fatalf("trial %d: solver says infeasible but enumeration found %v", trial, enum)
			}
			continue
		}
		if err := VerifySolution(A, b, sol); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		// Every enumerated solution must lie in the returned lattice.
		for _, x := range enum {
			if !sol.InLattice(x) {
				t.Fatalf("trial %d: enumerated solution %v not in lattice", trial, x)
			}
		}
		// The lattice must produce every box solution: count points of
		// X0 + B*z inside the box and compare with the enumeration.
		zdim := sol.B.Cols
		count := 0
		z := make([]*big.Int, zdim)
		for j := range z {
			z[j] = new(big.Int)
		}
		// Rigorous bound on |z| for lattice points in the box:
		// z = (U^{-1}(x - X0))[r:], so |z_j| <= sum_i |U^{-1}[r+j][i]| * (bound + |X0_i|).
		n2 := sol.U.Cols
		maxX0 := new(big.Int)
		for _, v := range sol.X0 {
			if a := new(big.Int).Abs(v); a.Cmp(maxX0) > 0 {
				maxX0.Set(a)
			}
		}
		rhs := new(big.Rat).SetInt(new(big.Int).Add(maxX0, big.NewInt(bound)))
		zboundRat := new(big.Rat)
		for j := sol.Rank; j < n2; j++ {
			e := make([]*big.Rat, n2)
			for i := range e {
				e[i] = new(big.Rat)
			}
			e[j].SetInt64(1)
			w, ok := ratSolve(sol.U, e)
			if !ok {
				t.Fatalf("trial %d: U not invertible", trial)
			}
			rowSum := new(big.Rat)
			for i := 0; i < n2; i++ {
				rowSum.Add(rowSum, new(big.Rat).Abs(w[i]))
			}
			rowSum.Mul(rowSum, rhs)
			if rowSum.Cmp(zboundRat) > 0 {
				zboundRat.Set(rowSum)
			}
		}
		zbound := new(big.Int).Quo(zboundRat.Num(), zboundRat.Denom()).Int64() + 1
		var rec func(k int)
		rec = func(k int) {
			if k == zdim {
				x := sol.LatticePoint(z)
				in := true
				for _, xi := range x {
					if xi.Cmp(big.NewInt(bound)) > 0 || xi.Cmp(big.NewInt(-bound)) < 0 {
						in = false
						break
					}
				}
				if in {
					count++
				}
				return
			}
			for v := -zbound; v <= zbound; v++ {
				z[k].SetInt64(v)
				rec(k + 1)
			}
		}
		work := int64(1)
		for k := 0; k < zdim && work <= 5_000_000; k++ {
			work *= 2*zbound + 1
		}
		if zdim == 0 {
			work = 1
		}
		if work <= 5_000_000 {
			rec(0)
			if count != len(enum) {
				t.Fatalf("trial %d: lattice has %d points in box, enumeration found %d", trial, count, len(enum))
			}
		}
		// Random non-solutions must be rejected by InLattice.
		for k := 0; k < 4; k++ {
			y := randVec(rng, n, bound)
			if vecEqual(A.MulVec(y), b) {
				continue
			}
			if sol.InLattice(y) {
				t.Fatalf("trial %d: InLattice accepts non-solution %v", trial, y)
			}
		}
	}
}

func TestBigIntegers(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		m, n := 4, 6
		A := NewMat(m, n)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				A.Data[i*n+j].Rand(rng, new(big.Int).Lsh(big.NewInt(1), 60))
				if rng.Intn(2) == 0 {
					A.Data[i*n+j].Neg(A.Data[i*n+j])
				}
			}
		}
		b := make([]*big.Int, m)
		for i := range b {
			b[i] = new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 60))
		}
		sol, inf := Solve(A, b)
		if sol == nil {
			if err := VerifyInfeasible(A, b, inf); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			continue
		}
		if err := VerifySolution(A, b, sol); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestConsistentRandomAlwaysFeasible(t *testing.T) {
	// Build b = A*x0 with random integer x0: the system is feasible by
	// construction, and x0 must lie in the returned lattice.
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		m := 1 + int(rng.Intn(5))
		n := 1 + int(rng.Intn(6))
		A := randMat(rng, m, n, 4)
		x0 := randVec(rng, n, 6)
		b := A.MulVec(x0)
		sol, inf := Solve(A, b)
		if sol == nil {
			t.Fatalf("trial %d: feasible system reported infeasible, cert %v", trial, inf.C)
		}
		if err := VerifySolution(A, b, sol); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !sol.InLattice(x0) {
			t.Fatalf("trial %d: constructed solution not in lattice", trial)
		}
	}
}

func ExampleSolve() {
	A := matFromInts([][]int64{{2, 1, -1}, {1, 3, 2}})
	b := vecFromInts(8, -1)
	sol, _ := Solve(A, b)
	fmt.Println("x0:", sol.X0)
	fmt.Println("rank:", sol.Rank)
	if err := VerifySolution(A, b, sol); err != nil {
		fmt.Println("verify failed:", err)
	} else {
		fmt.Println("verified")
	}
	// Output:
	// x0: [0 3 -5]
	// rank: 2
	// verified
}
