package ils

import (
	"errors"
	"math/big"
)

// Solution is a certified integer solution of A x = b.
//
// Every solution of the system is exactly X0 plus an integer combination of
// the Kernel basis vectors; VerifySolution checks all of these properties.
type Solution struct {
	// X0 is one particular integer solution, A*X0 = b. Length n.
	X0 []*big.Int
	// Kernel is a Z-basis of the lattice {x in Z^n : A x = 0}. It spans
	// the *whole* kernel lattice (not a proper sublattice) and has
	// n - rank(A) vectors of length n.
	Kernel [][]*big.Int
}

// Certificate proves that A x = b has no integer solution.
//
// It encodes a rational row vector y = Num/Den (Num has length m, Den > 0)
// such that y*A is an integer vector but y*b is not an integer. If an
// integer solution x existed, y*b = y*A*x would be integral - contradiction.
type Certificate struct {
	Num []*big.Int
	Den *big.Int
}

// Solve solves A x = b over the integers.
//
// Exactly one of the returned Solution or Certificate is non-nil (err is
// reserved for malformed input such as a dimension mismatch).
//
// Method: let B = A^T and compute R = V*B in row-echelon form with V
// unimodular (see echelon). Then A*V^T = [H 0] where H = (top rows of R)^T
// has full column rank r = rank(A). Writing x = V^T [z; w], the system
// becomes H z = b with w free, so:
//   - the kernel lattice is spanned by rows r..n-1 of V,
//   - a particular solution is sum_j z_j * (row j of V),
//   - H z = b is solved by forward substitution over the pivot rows of R,
//     and any divisibility/consistency failure yields an explicit
//     infeasibility certificate y with yA in Z^n, yb not in Z.
func Solve(A *Matrix, b []*big.Int) (*Solution, *Certificate, error) {
	if A == nil || len(b) != A.Rows {
		return nil, nil, errors.New("ils: dimension mismatch between A and b")
	}
	m, n := A.Rows, A.Cols

	R, V, pivots := echelon(A.Transpose()) // R is n x m, V is n x n unimodular
	r := len(pivots)

	// Forward substitution for H z = b, where H[i][j] = R[j][i] and the
	// pivot row of column j is pivots[j] with diagonal R[j][pivots[j]] > 0.
	z := make([]*big.Int, r)
	for j := 0; j < r; j++ {
		cj := pivots[j]
		rem := new(big.Int).Set(b[cj])
		for jp := 0; jp < j; jp++ {
			rem.Sub(rem, new(big.Int).Mul(R.At(jp, cj), z[jp]))
		}
		q, mod := new(big.Int), new(big.Int)
		q.QuoRem(rem, R.At(j, cj), mod)
		if mod.Sign() != 0 {
			return nil, divisibilityCertificate(R, pivots, j), nil
		}
		z[j] = q
	}

	// Consistency check on the non-pivot rows of A.
	inPivot := make([]bool, m)
	for _, c := range pivots {
		inPivot[c] = true
	}
	for i := 0; i < m; i++ {
		if inPivot[i] {
			continue
		}
		rem := new(big.Int).Set(b[i])
		for j := 0; j < r; j++ {
			rem.Sub(rem, new(big.Int).Mul(R.At(j, i), z[j]))
		}
		if rem.Sign() != 0 {
			return nil, consistencyCertificate(R, pivots, i, rem), nil
		}
	}

	// Particular solution x0 = V^T [z; 0] = sum_j z_j * (row j of V).
	x0 := make([]*big.Int, n)
	for col := 0; col < n; col++ {
		acc := new(big.Int)
		for j := 0; j < r; j++ {
			acc.Add(acc, new(big.Int).Mul(z[j], V.At(j, col)))
		}
		x0[col] = acc
	}

	// Kernel basis: rows r..n-1 of V (columns r..n-1 of V^T).
	kernel := make([][]*big.Int, 0, n-r)
	for i := r; i < n; i++ {
		kernel = append(kernel, V.Row(i))
	}
	return &Solution{X0: x0, Kernel: kernel}, nil, nil
}

// divisibilityCertificate builds y from a failed division at substitution
// step j: rem_j is not divisible by d = R[j][pivots[j]]. Take y supported on
// the pivot rows with y*H = e_j (the j-th unit row vector): then y*A is
// integral, while y*b = rem_j/d is not an integer. With yc[k] = y[pivots[k]]
// one has (y*H)[jp] = sum_k yc[k] * R[jp][pivots[k]], and the matrix
// P[k][jp] = R[jp][pivots[k]] is upper triangular (P[k][jp] = 0 for k < jp)
// with positive diagonal, so yc = e_j * P^-1 by back-substitution. Entries
// of yc above index j come out zero, as expected.
func divisibilityCertificate(R *Matrix, pivots []int, j int) *Certificate {
	m := R.Cols
	r := len(pivots)
	yc := make([]*big.Rat, r)
	for jp := r - 1; jp >= 0; jp-- {
		num := new(big.Rat)
		if jp == j {
			num.SetInt64(1)
		}
		for k := jp + 1; k < r; k++ {
			if yc[k].Sign() == 0 {
				continue
			}
			t := new(big.Rat).SetInt(R.At(jp, pivots[k]))
			num.Sub(num, t.Mul(t, yc[k]))
		}
		yc[jp] = num.Quo(num, new(big.Rat).SetInt(R.At(jp, pivots[jp])))
	}
	y := make([]*big.Rat, m)
	for k := 0; k < r; k++ {
		y[pivots[k]] = yc[k]
	}
	return ratVecToCertificate(y)
}

// consistencyCertificate builds y from a failed non-pivot row: row i of A is
// linearly dependent on the pivot rows but b[i] differs from the implied
// value by rho != 0. The vector N = e_i - sum_j q_j e_{pivots[j]} (with q
// chosen so N*H = 0) satisfies N*A = 0 and N*b = rho, so y = N/(2*rho) has
// y*A = 0 in Z^n and y*b = 1/2 not in Z.
//
// (N*H)[jp] = R[jp][row] - sum_k q_k R[jp][pivots[k]], and the matrix
// P[k][jp] = R[jp][pivots[k]] is upper triangular in (k, jp) with positive
// diagonal, so q is found by back-substitution from the last pivot down.
func consistencyCertificate(R *Matrix, pivots []int, row int, rho *big.Int) *Certificate {
	m := R.Cols
	r := len(pivots)
	q := make([]*big.Rat, r)
	for j := r - 1; j >= 0; j-- {
		num := new(big.Rat).SetInt(R.At(j, row))
		for k := j + 1; k < r; k++ {
			if q[k].Sign() == 0 {
				continue
			}
			t := new(big.Rat).SetInt(R.At(j, pivots[k]))
			num.Sub(num, t.Mul(t, q[k]))
		}
		q[j] = num.Quo(num, new(big.Rat).SetInt(R.At(j, pivots[j])))
	}
	scale := new(big.Rat).SetInt(new(big.Int).Mul(rho, big.NewInt(2)))
	y := make([]*big.Rat, m)
	y[row] = new(big.Rat).Quo(big.NewRat(1, 1), scale)
	for j := 0; j < r; j++ {
		t := new(big.Rat).Neg(q[j])
		y[pivots[j]] = t.Quo(t, scale)
	}
	return ratVecToCertificate(y)
}

// ratVecToCertificate clears denominators: y = Num/Den with Den > 0.
func ratVecToCertificate(y []*big.Rat) *Certificate {
	den := big.NewInt(1)
	tmp := new(big.Int)
	for _, v := range y {
		if v == nil {
			continue
		}
		d := new(big.Int).Set(v.Denom())
		tmp.GCD(nil, nil, den, d)
		d.Quo(d, tmp)
		den.Mul(den, d)
	}
	num := make([]*big.Int, len(y))
	for i, v := range y {
		num[i] = new(big.Int)
		if v != nil {
			num[i].Mul(v.Num(), tmp.Quo(den, v.Denom()))
		}
	}
	return &Certificate{Num: num, Den: den}
}
