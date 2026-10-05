package main

import "math/big"

// smithNormalForm computes a diagonal reduction of the integer m x n matrix A:
// it returns unimodular matrices U (m x m) and V (n x n) and a diagonal
// matrix S (m x n) such that U*A*V = S. The nonzero diagonal entries occupy
// S[0][0] .. S[r-1][r-1] where r is the rank of A.
//
// This "diagonal form" (without enforcing the divisibility chain between
// diagonal entries) is sufficient to decide integer solvability of A*x = b
// and to parameterize all integer solutions.
func smithNormalForm(A [][]*big.Int, m, n int) (U, S, V [][]*big.Int) {
	S = copyMat(A)
	U = identityMat(m)
	V = identityMat(n)

	for k := 0; k < m && k < n; k++ {
		// Find the nonzero entry of smallest absolute value in the
		// trailing submatrix to use as pivot.
		pi, pj := -1, -1
		var best *big.Int
		for i := k; i < m; i++ {
			for j := k; j < n; j++ {
				if S[i][j].Sign() == 0 {
					continue
				}
				a := new(big.Int).Abs(S[i][j])
				if pi == -1 || a.Cmp(best) < 0 {
					pi, pj, best = i, j, a
				}
			}
		}
		if pi == -1 {
			break // remaining submatrix is zero
		}
		swapRows(S, k, pi)
		swapRows(U, k, pi)
		swapCols(S, k, pj)
		swapCols(V, k, pj)

		for {
			reduced := true
			// Eliminate column k below the pivot.
			for i := k + 1; i < m; i++ {
				if S[i][k].Sign() == 0 {
					continue
				}
				q, r := new(big.Int), new(big.Int)
				q.QuoRem(S[i][k], S[k][k], r)
				if q.Sign() != 0 {
					rowSubMul(S, i, k, q)
					rowSubMul(U, i, k, q)
				}
				if r.Sign() != 0 {
					// Nonzero remainder: a smaller pivot appears.
					swapRows(S, i, k)
					swapRows(U, i, k)
					reduced = false
					break
				}
			}
			if !reduced {
				continue
			}
			// Eliminate row k right of the pivot. Column operations do
			// not disturb the already-cleared column k, because the
			// entries of column k below the pivot are zero.
			for j := k + 1; j < n; j++ {
				if S[k][j].Sign() == 0 {
					continue
				}
				q, r := new(big.Int), new(big.Int)
				q.QuoRem(S[k][j], S[k][k], r)
				if q.Sign() != 0 {
					colSubMul(S, j, k, q)
					colSubMul(V, j, k, q)
				}
				if r.Sign() != 0 {
					swapCols(S, j, k)
					swapCols(V, j, k)
					reduced = false
					break
				}
			}
			if reduced {
				break
			}
		}
	}
	return U, S, V
}

// inverseUnimodular computes the exact inverse of a unimodular integer
// matrix via fraction-free-style Gauss-Jordan elimination over the
// rationals. It panics if the matrix is singular or not unimodular.
func inverseUnimodular(M [][]*big.Int) [][]*big.Int {
	n := len(M)
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, 2*n)
		for j := 0; j < n; j++ {
			a[i][j] = new(big.Rat).SetInt(M[i][j])
		}
		for j := 0; j < n; j++ {
			a[i][n+j] = new(big.Rat)
		}
		a[i][n+i].SetInt64(1)
	}
	for col := 0; col < n; col++ {
		piv := -1
		for i := col; i < n; i++ {
			if a[i][col].Sign() != 0 {
				piv = i
				break
			}
		}
		if piv == -1 {
			panic("inverseUnimodular: singular matrix")
		}
		a[col], a[piv] = a[piv], a[col]
		d := new(big.Rat).Inv(a[col][col])
		for j := range a[col] {
			a[col][j].Mul(a[col][j], d)
		}
		for i := 0; i < n; i++ {
			if i == col || a[i][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[i][col])
			for j := range a[i] {
				a[i][j].Sub(a[i][j], new(big.Rat).Mul(f, a[col][j]))
			}
		}
	}
	one := big.NewInt(1)
	inv := make([][]*big.Int, n)
	for i := range inv {
		inv[i] = make([]*big.Int, n)
		for j := 0; j < n; j++ {
			r := a[i][n+j]
			if r.Denom().Cmp(one) != 0 {
				panic("inverseUnimodular: matrix is not unimodular")
			}
			inv[i][j] = new(big.Int).Set(r.Num())
		}
	}
	return inv
}
