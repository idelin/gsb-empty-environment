package ilse

import "math/big"

// Solution is a certified description of the integer solution set of A x = b:
//
//	x = X0 + B*z,  z in Z^(n-rank)
//
// The remaining fields form a completeness certificate: U is unimodular,
// A*U = [H | 0] with H of full column rank, H*Y = b, X0 = U*[Y;0] and
// B = U[:, rank:]. Any verifier can re-check these identities to conclude
// that X0 + B*Z^(n-rank) is EXACTLY the solution set, not a subset.
type Solution struct {
	X0   []*big.Int // particular integer solution, length n
	B    *Mat       // n x (n-rank) basis of the homogeneous lattice {x: A x = 0}
	U    *Mat       // n x n unimodular certificate, A*U = [H | 0]
	H    *Mat       // m x rank, full column rank
	Y    []*big.Int // length rank, H*Y = b
	Rank int
}

// Infeasible is a certificate that A x = b has no integer solution.
// C is a rational row vector such that, writing g for the (rational) gcd of
// the entries of C*A (so C*A*Z^n = g*Z), the value C*b does not lie in g*Z.
// Hence C*A*x = C*b is impossible for integer x, and a fortiori A*x = b is.
type Infeasible struct {
	C []*big.Rat
}

// Solve returns either a certified Solution or a certified Infeasible.
// Exactly one of the two return values is non-nil.
func Solve(A *Mat, b []*big.Int) (*Solution, *Infeasible) {
	if A.Rows != len(b) {
		panic("ilse: dimension mismatch between A and b")
	}
	m, n := A.Rows, A.Cols

	// Column-style Hermite reduction: M = A*U, U unimodular, M = [H | 0]
	// where the pivot rows of H form an upper-triangular nonsingular block.
	M := A.Copy()
	U := Eye(n)
	prow := []int{} // prow[j] = pivot row of the j-th nonzero column of M
	r := 0
	for i := 0; i < m && r < n; i++ {
		j := -1
		for k := r; k < n; k++ {
			if M.At(i, k).Sign() != 0 {
				j = k
				break
			}
		}
		if j < 0 {
			continue
		}
		M.swapCols(j, r)
		U.swapCols(j, r)
		for k := r + 1; k < n; k++ {
			if M.At(i, k).Sign() == 0 {
				continue
			}
			a := M.At(i, r)
			bb := M.At(i, k)
			g := new(big.Int)
			s := new(big.Int)
			t := new(big.Int)
			g.GCD(s, t, a, bb) // s*a + t*bb = g, g >= 0
			u := new(big.Int).Quo(a, g)
			v := new(big.Int).Quo(bb, g)
			// det [[s, -v], [t, u]] = s*u + t*v = (s*a + t*bb)/g = 1
			M.combineCols(r, k, s, t, v, u)
			U.combineCols(r, k, s, t, v, u)
		}
		prow = append(prow, i)
		r++
	}

	H := M.SubCols(0, r)

	// T[j'][j] = H[prow[j']][j] is lower-triangular with nonzero diagonal.
	T := NewMat(r, r)
	for jj := 0; jj < r; jj++ {
		for kk := 0; kk < r; kk++ {
			T.Set(jj, kk, H.At(prow[jj], kk))
		}
	}
	btilde := make([]*big.Int, r)
	for j := 0; j < r; j++ {
		btilde[j] = new(big.Int).Set(b[prow[j]])
	}

	// Forward substitution on the lower-triangular system T*y = btilde.
	y := make([]*big.Int, r)
	rem := new(big.Int)
	for j := 0; j < r; j++ {
		rhs := new(big.Int).Set(btilde[j])
		for k := 0; k < j; k++ {
			rhs.Sub(rhs, new(big.Int).Mul(T.At(j, k), y[k]))
		}
		diag := T.At(j, j)
		q := new(big.Int)
		q.QuoRem(rhs, diag, rem)
		if rem.Sign() != 0 {
			// y_j is forced to the non-integer rhs/diag: infeasible.
			// Certificate c = e_j^T * T^{-1} mapped back to rows of A:
			// c*A = [e_j | 0]*U^{-1} is integral, but c*b = rhs/diag is not.
			w := triInvRow(T, j)
			c := make([]*big.Rat, m)
			for i := 0; i < m; i++ {
				c[i] = new(big.Rat)
			}
			for k := 0; k < r; k++ {
				c[prow[k]].Add(c[prow[k]], w[k])
			}
			return nil, &Infeasible{C: c}
		}
		y[j] = q
	}

	// Check the remaining (non-pivot) rows of H*y = b.
	Hy := H.MulVec(y)
	for i := 0; i < m; i++ {
		if Hy[i].Cmp(b[i]) == 0 {
			continue
		}
		// b is outside the rational column span of H: there is a rational
		// c with c*A = 0 and c*b != 0. Take
		//   c = e_i - sum_j alpha_j e_prow[j],  alpha^T T = H[i, :r].
		alpha := triInvComb(T, H, i)
		c := make([]*big.Rat, m)
		for k := 0; k < m; k++ {
			c[k] = new(big.Rat)
		}
		c[i].SetInt64(1)
		for j := 0; j < r; j++ {
			c[prow[j]].Sub(c[prow[j]], alpha[j])
		}
		return nil, &Infeasible{C: c}
	}

	// Assemble the solution.
	X0 := U.SubCols(0, r).MulVec(y)
	B := U.SubCols(r, n)
	return &Solution{X0: X0, B: B, U: U, H: H, Y: y, Rank: r}, nil
}

// triInvRow returns row j of T^{-1} as rationals, for a nonsingular
// lower-triangular T. It solves w^T T = e_j^T.
func triInvRow(T *Mat, j int) []*big.Rat {
	r := T.Cols
	w := make([]*big.Rat, r)
	for k := 0; k < r; k++ {
		w[k] = new(big.Rat)
	}
	// T is lower triangular: equation k involves w[q] for q >= k,
	// so solve from the last column backwards.
	for k := r - 1; k >= 0; k-- {
		rhs := new(big.Rat)
		if k == j {
			rhs.SetInt64(1)
		}
		for q := k + 1; q < r; q++ {
			t := new(big.Rat).Mul(w[q], new(big.Rat).SetInt(T.At(q, k)))
			rhs.Sub(rhs, t)
		}
		w[k].Quo(rhs, new(big.Rat).SetInt(T.At(k, k)))
	}
	return w
}

// triInvComb returns alpha with alpha^T T = H[row, :r], as rationals.
func triInvComb(T *Mat, H *Mat, row int) []*big.Rat {
	r := T.Cols
	alpha := make([]*big.Rat, r)
	for k := r - 1; k >= 0; k-- {
		rhs := new(big.Rat).SetInt(H.At(row, k))
		for q := k + 1; q < r; q++ {
			t := new(big.Rat).Mul(alpha[q], new(big.Rat).SetInt(T.At(q, k)))
			rhs.Sub(rhs, t)
		}
		alpha[k] = new(big.Rat).Quo(rhs, new(big.Rat).SetInt(T.At(k, k)))
	}
	return alpha
}
