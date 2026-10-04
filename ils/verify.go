package ils

import (
	"errors"
	"fmt"
	"math/big"
)

// VerifySolution independently checks a claimed Solution of A x = b:
//
//  1. A*X0 = b holds exactly (all arithmetic is over big.Int);
//  2. every kernel basis vector k satisfies A*k = 0;
//  3. the basis is complete: it has n - rank(A) linearly independent
//     vectors, and the lattice they generate is saturated, i.e. the gcd
//     of all maximal minors of the basis matrix is 1 (equivalently all
//     Smith invariant factors are 1). Hence the basis spans the *entire*
//     kernel lattice {x in Z^n : A x = 0} and not a proper sublattice.
//
// Note that only integer data is accepted: a rational "solution" cannot
// even be represented in a Solution, let alone verified.
func VerifySolution(A *Matrix, b []*big.Int, sol *Solution) error {
	if A == nil || sol == nil || len(b) != A.Rows {
		return errors.New("ils: malformed input")
	}
	n := A.Cols
	if len(sol.X0) != n {
		return fmt.Errorf("ils: X0 has length %d, want %d", len(sol.X0), n)
	}

	// 1. A*X0 = b.
	Ax0 := A.MulVec(sol.X0)
	for i := 0; i < A.Rows; i++ {
		if Ax0[i].Cmp(b[i]) != 0 {
			return fmt.Errorf("ils: (A*X0)[%d] = %s != b[%d] = %s", i, Ax0[i], i, b[i])
		}
	}

	// 2. A*k = 0 for every basis vector.
	for idx, k := range sol.Kernel {
		if len(k) != n {
			return fmt.Errorf("ils: kernel vector %d has length %d, want %d", idx, len(k), n)
		}
		Ak := A.MulVec(k)
		for i := 0; i < A.Rows; i++ {
			if Ak[i].Sign() != 0 {
				return fmt.Errorf("ils: kernel vector %d is not in the kernel: (A*k)[%d] = %s", idx, i, Ak[i])
			}
		}
	}

	// 3a. Dimension: #basis vectors must equal n - rank(A).
	_, _, pivA := echelon(A.Transpose())
	want := n - len(pivA)
	if len(sol.Kernel) != want {
		return fmt.Errorf("ils: kernel basis has %d vectors, want n - rank(A) = %d", len(sol.Kernel), want)
	}

	// 3b. Saturation: the basis matrix (rows = basis vectors) must have
	// full row rank, and the gcd of its maximal minors must be 1.
	if want > 0 {
		K := NewMatrix(want, n)
		for i, k := range sol.Kernel {
			for j := 0; j < n; j++ {
				K.Set(i, j, k[j])
			}
		}
		_, _, pivK := echelon(K)
		if len(pivK) != want {
			return errors.New("ils: kernel basis vectors are linearly dependent")
		}
		if g := maximalMinorGCD(sol.Kernel, n); g.Cmp(big.NewInt(1)) != 0 {
			return fmt.Errorf("ils: kernel lattice is a proper sublattice of ker(A): maximal minor gcd is %s, not 1", g)
		}
	}
	return nil
}

// VerifyUnsat checks an infeasibility certificate: with y = Num/Den it
// verifies that y*A is componentwise integral while y*b is not, which
// proves A x = b has no integer solution.
func VerifyUnsat(A *Matrix, b []*big.Int, cert *Certificate) error {
	if A == nil || cert == nil || len(b) != A.Rows || len(cert.Num) != A.Rows {
		return errors.New("ils: malformed input")
	}
	if cert.Den == nil || cert.Den.Sign() <= 0 {
		return errors.New("ils: certificate denominator must be positive")
	}
	rem := new(big.Int)
	acc := new(big.Int)
	for j := 0; j < A.Cols; j++ {
		acc.SetInt64(0)
		for i := 0; i < A.Rows; i++ {
			acc.Add(acc, rem.Mul(cert.Num[i], A.At(i, j)))
		}
		if rem.Rem(acc, cert.Den).Sign() != 0 {
			return fmt.Errorf("ils: (y*A)[%d] = %s/%s is not integral", j, acc, cert.Den)
		}
	}
	acc.SetInt64(0)
	for i := 0; i < A.Rows; i++ {
		acc.Add(acc, rem.Mul(cert.Num[i], b[i]))
	}
	if rem.Rem(acc, cert.Den).Sign() == 0 {
		return fmt.Errorf("ils: y*b = %s/%s is integral, certificate proves nothing", acc, cert.Den)
	}
	return nil
}
