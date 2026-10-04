package ratls

import (
	"errors"
	"fmt"
	"math/big"
)

// checkNormalEquations verifies (A^T A) x = A^T b exactly.
func checkNormalEquations(ata *Matrix, atb, x []*big.Rat, bud budget) error {
	lhs, err := matVec(ata, x, bud)
	if err != nil {
		return err
	}
	if !vecEqual(lhs, atb) {
		return errors.New("ratls: verification failed: normal equations do not hold")
	}
	return nil
}

// checkNullOfA verifies A v = 0 for every basis vector. This is a
// strictly stronger, independent check than (A^T A) v = 0.
func checkNullOfA(A *Matrix, nullBasis [][]*big.Rat, bud budget) error {
	for j, v := range nullBasis {
		av, err := matVec(A, v, bud)
		if err != nil {
			return err
		}
		if !vecIsZero(av) {
			return fmt.Errorf("ratls: verification failed: null basis vector %d is not in null(A)", j)
		}
	}
	return nil
}

// checkOrthogonal verifies v is orthogonal to every null basis vector,
// the defining property of the minimum-norm solution.
func checkOrthogonal(nullBasis [][]*big.Rat, v []*big.Rat, bud budget) error {
	for j, nv := range nullBasis {
		d, err := bud.dot(nv, v)
		if err != nil {
			return err
		}
		if d.Sign() != 0 {
			return fmt.Errorf("ratls: verification failed: min-norm solution not orthogonal to null basis vector %d", j)
		}
	}
	return nil
}

// Verify independently re-derives every claimed property of sol from
// scratch (recomputing A^T A, A^T b and rank(A)) and checks them
// exactly. It returns nil iff all of the following hold:
//
//  1. Particular satisfies the normal equations (A^T A) x = A^T b.
//  2. Every NullBasis vector lies in null(A) (not merely null(A^T A)).
//  3. rank(A) + len(NullBasis) = A.Cols, so the affine family
//     Particular + span(NullBasis) covers all minimizers.
//  4. MinNorm satisfies the normal equations.
//  5. MinNorm is orthogonal to null(A), making it the unique
//     minimum Euclidean norm minimizer.
//
// budgetBits bounds coefficient bit lengths during verification;
// 0 means unbounded.
func Verify(A *Matrix, b []*big.Rat, sol *Solution, budgetBits int) error {
	if A.Rows != len(b) {
		return fmt.Errorf("ratls: A has %d rows but b has %d entries", A.Rows, len(b))
	}
	n := A.Cols
	if len(sol.Particular) != n || len(sol.MinNorm) != n {
		return errors.New("ratls: verification failed: solution vector has wrong length")
	}
	for _, v := range sol.NullBasis {
		if len(v) != n {
			return errors.New("ratls: verification failed: null basis vector has wrong length")
		}
	}
	bud := budget{budgetBits}

	at := transpose(A)
	ata, err := matMul(at, A, bud)
	if err != nil {
		return err
	}
	atb, err := matVec(at, b, bud)
	if err != nil {
		return err
	}

	if err := checkNormalEquations(ata, atb, sol.Particular, bud); err != nil {
		return err
	}
	if err := checkNullOfA(A, sol.NullBasis, bud); err != nil {
		return err
	}

	// Independent to rank: compute rank(A) directly, independently of A^T A.
	_, pivots, err := rref(A, bud)
	if err != nil {
		return err
	}
	rank := len(pivots)
	if rank != sol.Rank {
		return fmt.Errorf("ratls: verification failed: claimed rank %d, actual rank(A) = %d", sol.Rank, rank)
	}
	if rank+len(sol.NullBasis) != n {
		return fmt.Errorf("ratls: verification failed: rank %d + nullity %d != %d columns", rank, len(sol.NullBasis), n)
	}

	if err := checkNormalEquations(ata, atb, sol.MinNorm, bud); err != nil {
		return err
	}
	return checkOrthogonal(sol.NullBasis, sol.MinNorm, bud)
}
