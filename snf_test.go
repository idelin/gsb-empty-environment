package main

import (
	"math/big"
	"math/rand"
	"testing"
)

func intMat(rows ...[]int64) [][]*big.Int {
	m := make([][]*big.Int, len(rows))
	for i, r := range rows {
		m[i] = make([]*big.Int, len(r))
		for j, v := range r {
			m[i][j] = big.NewInt(v)
		}
	}
	return m
}

func intVec(v ...int64) []*big.Int {
	out := make([]*big.Int, len(v))
	for i, x := range v {
		out[i] = big.NewInt(x)
	}
	return out
}

func matMul(A, B [][]*big.Int) [][]*big.Int {
	n := len(A)
	k := len(B[0])
	m := len(B)
	C := make([][]*big.Int, n)
	for i := range C {
		C[i] = make([]*big.Int, k)
		for j := range C[i] {
			C[i][j] = new(big.Int)
			for l := 0; l < m; l++ {
				C[i][j].Add(C[i][j], new(big.Int).Mul(A[i][l], B[l][j]))
			}
		}
	}
	return C
}

// detBareiss computes the exact determinant of a square integer matrix.
func detBareiss(M [][]*big.Int) *big.Int {
	n := len(M)
	A := copyMat(M)
	sign := int64(1)
	prev := big.NewInt(1)
	for k := 0; k < n-1; k++ {
		if A[k][k].Sign() == 0 {
			sw := -1
			for i := k + 1; i < n; i++ {
				if A[i][k].Sign() != 0 {
					sw = i
					break
				}
			}
			if sw == -1 {
				return big.NewInt(0)
			}
			A[k], A[sw] = A[sw], A[k]
			sign = -sign
		}
		for i := k + 1; i < n; i++ {
			for j := k + 1; j < n; j++ {
				t := new(big.Int).Mul(A[i][j], A[k][k])
				t.Sub(t, new(big.Int).Mul(A[i][k], A[k][j]))
				t.Quo(t, prev)
				A[i][j] = t
			}
		}
		prev = A[k][k]
	}
	d := new(big.Int).Set(A[n-1][n-1])
	if sign < 0 {
		d.Neg(d)
	}
	return d
}

func TestSmithNormalForm(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		m := 1 + rng.Intn(5)
		n := 1 + rng.Intn(5)
		A := make([][]*big.Int, m)
		for i := range A {
			A[i] = make([]*big.Int, n)
			for j := range A[i] {
				A[i][j] = big.NewInt(int64(rng.Intn(11) - 5))
			}
		}
		U, S, V := smithNormalForm(A, m, n)

		// U*A*V == S
		got := matMul(matMul(U, A), V)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				if got[i][j].Cmp(S[i][j]) != 0 {
					t.Fatalf("trial %d: U*A*V != S at (%d,%d)", trial, i, j)
				}
			}
		}
		// S diagonal, nonzero entries first
		seenZero := false
		for i := 0; i < m && i < n; i++ {
			if S[i][i].Sign() == 0 {
				seenZero = true
			} else if seenZero {
				t.Fatalf("trial %d: nonzero diagonal after zero", trial)
			}
			for j := 0; j < n; j++ {
				if i != j && S[i][j].Sign() != 0 {
					t.Fatalf("trial %d: S not diagonal at (%d,%d)", trial, i, j)
				}
			}
		}
		for i := n; i < m; i++ {
			for j := 0; j < n; j++ {
				if S[i][j].Sign() != 0 {
					t.Fatalf("trial %d: nonzero below diagonal block", trial)
				}
			}
		}
		// U and V unimodular
		if d := detBareiss(U); d.Cmp(big.NewInt(1)) != 0 && d.Cmp(big.NewInt(-1)) != 0 {
			t.Fatalf("trial %d: det(U) = %s", trial, d)
		}
		if d := detBareiss(V); d.Cmp(big.NewInt(1)) != 0 && d.Cmp(big.NewInt(-1)) != 0 {
			t.Fatalf("trial %d: det(V) = %s", trial, d)
		}
		// V*Vinv == I
		Vinv := inverseUnimodular(V)
		I := matMul(V, Vinv)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				want := int64(0)
				if i == j {
					want = 1
				}
				if I[i][j].Cmp(big.NewInt(want)) != 0 {
					t.Fatalf("trial %d: V*Vinv != I at (%d,%d)", trial, i, j)
				}
			}
		}
	}
}
