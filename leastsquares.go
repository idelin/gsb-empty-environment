package ratls

import (
	"errors"
	"fmt"
	"math/big"
)

// Solution describes all minimizers of ||A x - b||_2 over Q.
//
// The full affine set of least-squares solutions is
//
//	Particular + span(NullBasis)
//
// and MinNorm is the unique element of that set with the smallest
// Euclidean norm. All vectors have length A.Cols. NullBasis is empty
// exactly when A has full column rank.
type Solution struct {
	Rank       int          // rank of A (equivalently of A^T A)
	Particular []*big.Rat   // one least-squares solution (free variables = 0)
	NullBasis  [][]*big.Rat // basis of null(A); coefficient vectors of the affine family
	MinNorm    []*big.Rat   // unique minimum Euclidean norm least-squares solution
}

// LeastSquares solves min_x ||A x - b||_2 exactly over the rationals.
//
// budgetBits bounds the bit length (max of numerator and denominator)
// of any coefficient produced during the computation; 0 means
// unbounded. A *BudgetError is returned when the budget is exceeded.
func LeastSquares(A *Matrix, b []*big.Rat, budgetBits int) (*Solution, error) {
	if A.Rows != len(b) {
		return nil, fmt.Errorf("ratls: A has %d rows but b has %d entries", A.Rows, len(b))
	}
	bud := budget{budgetBits}
	n := A.Cols

	// Normal equations: (A^T A) x = A^T b, always consistent.
	at := transpose(A)
	ata, err := matMul(at, A, bud)
	if err != nil {
		return nil, err
	}
	atb, err := matVec(at, b, bud)
	if err != nil {
		return nil, err
	}
	particular, nullBasis, err := solveConsistent(ata, atb, bud)
	if err != nil {
		return nil, err
	}

	sol := &Solution{
		Rank:       n - len(nullBasis),
		Particular: particular,
		NullBasis:  nullBasis,
	}

	// Independent exact checks before deriving the min-norm solution.
	if err := checkNormalEquations(ata, atb, particular, bud); err != nil {
		return nil, err
	}
	if err := checkNullOfA(A, nullBasis, bud); err != nil {
		return nil, err
	}

	minNorm, err := minNormSolution(particular, nullBasis, bud)
	if err != nil {
		return nil, err
	}
	sol.MinNorm = minNorm

	if err := checkNormalEquations(ata, atb, minNorm, bud); err != nil {
		return nil, err
	}
	if err := checkOrthogonal(nullBasis, minNorm, bud); err != nil {
		return nil, err
	}
	return sol, nil
}

// minNormSolution finds the unique minimum-norm point of the affine
// set xp + span(nullBasis) by projecting xp onto the orthogonal
// complement of the null space: with N = [nullBasis...], solve the
// Gram system (N^T N) t = -N^T xp and return xp + N t.
func minNormSolution(xp []*big.Rat, nullBasis [][]*big.Rat, bud budget) ([]*big.Rat, error) {
	k := len(nullBasis)
	if k == 0 {
		return copyVec(xp), nil
	}
	n := len(xp)
	gram := NewMatrix(k, k)
	rhs := make([]*big.Rat, k)
	for i := 0; i < k; i++ {
		di, err := bud.dot(nullBasis[i], xp)
		if err != nil {
			return nil, err
		}
		rhs[i] = new(big.Rat).Neg(di)
		for j := 0; j <= i; j++ {
			g, err := bud.dot(nullBasis[i], nullBasis[j])
			if err != nil {
				return nil, err
			}
			gram.Set(i, j, g)
			gram.Set(j, i, g)
		}
	}
	t, nullOfGram, err := solveConsistent(gram, rhs, bud)
	if err != nil {
		return nil, err
	}
	if len(nullOfGram) != 0 {
		return nil, errors.New("ratls: internal error: Gram matrix of null basis is singular")
	}
	out := copyVec(xp)
	for j := 0; j < k; j++ {
		if t[j].Sign() == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			p, err := bud.mul(nullBasis[j][i], t[j])
			if err != nil {
				return nil, err
			}
			out[i], err = bud.add(out[i], p)
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
