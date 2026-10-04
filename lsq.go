package ratlsq

import (
	"fmt"
	"math/big"
	"strings"
)

// rref reduces m to reduced row echelon form by exact Gauss-Jordan
// elimination and returns the reduced matrix together with the pivot
// column indices. The input is not modified.
func rref(m *Matrix, budget Budget) (*Matrix, []int, error) {
	a := m.Clone()
	var pivots []int
	rank := 0
	tmp := new(big.Rat)
	for col := 0; col < a.Cols && rank < a.Rows; col++ {
		piv := -1
		for r := rank; r < a.Rows; r++ {
			if a.get(r, col).Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		a.swapRows(rank, piv)
		inv := new(big.Rat).Inv(a.get(rank, col))
		for c := col; c < a.Cols; c++ {
			tmp.Mul(a.get(rank, c), inv)
			if err := budget.check("rref normalize", tmp); err != nil {
				return nil, nil, err
			}
			a.set(rank, c, tmp)
		}
		for r := 0; r < a.Rows; r++ {
			if r == rank {
				continue
			}
			if a.get(r, col).Sign() == 0 {
				continue
			}
			factor := new(big.Rat).Set(a.get(r, col))
			for c := col; c < a.Cols; c++ {
				tmp.Mul(factor, a.get(rank, c))
				tmp.Neg(tmp)
				tmp.Add(tmp, a.get(r, c))
				if err := budget.check("rref eliminate", tmp); err != nil {
					return nil, nil, err
				}
				a.set(r, c, tmp)
			}
		}
		pivots = append(pivots, col)
		rank++
	}
	return a, pivots, nil
}

// nullSpace extracts a basis of the null space from an RREF matrix and
// its pivot columns: each free column f contributes the vector with
// x[f] = 1 and x[pivot_i] = -rref[i][f].
func nullSpace(r *Matrix, pivots []int) []*Vector {
	isPivot := make([]bool, r.Cols)
	for _, p := range pivots {
		isPivot[p] = true
	}
	var basis []*Vector
	for f := 0; f < r.Cols; f++ {
		if isPivot[f] {
			continue
		}
		v := NewVector(r.Cols)
		v.Set(f, big.NewRat(1, 1))
		for i, p := range pivots {
			v.Set(p, new(big.Rat).Neg(r.get(i, f)))
		}
		basis = append(basis, v)
	}
	return basis
}

// solveLinear solves m*x = rhs exactly. m may be rectangular or singular.
// It returns one particular solution and a basis of null(m), so that all
// solutions are particular + span(basis). ErrInconsistent is returned if
// the system has no solution.
func solveLinear(m *Matrix, rhs *Vector, budget Budget) (*Vector, []*Vector, error) {
	if m.Rows != rhs.Len() {
		return nil, nil, ErrDimension
	}
	aug := NewMatrix(m.Rows, m.Cols+1)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			aug.set(i, j, m.get(i, j))
		}
		aug.set(i, m.Cols, rhs.At(i))
	}
	r, pivots, err := rref(aug, budget)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pivots {
		if p == m.Cols {
			return nil, nil, ErrInconsistent
		}
	}
	particular := NewVector(m.Cols)
	for i, p := range pivots {
		particular.Set(p, r.get(i, m.Cols))
	}
	coeff := NewMatrix(len(pivots), m.Cols)
	for i := range pivots {
		for j := 0; j < m.Cols; j++ {
			coeff.set(i, j, r.get(i, j))
		}
	}
	return particular, nullSpace(coeff, pivots), nil
}

// Solution describes all exact least-squares solutions of min ||A x - b||.
//
// The full solution set is the affine space
//
//	x = Particular + t1*NullBasis[0] + ... + tk*NullBasis[k-1],  ti in Q
//
// where NullBasis spans null(A). NullBasis is empty iff the solution is
// unique. MinNorm is the unique solution of minimal Euclidean norm.
type Solution struct {
	Rank       int       // rank of A
	Particular *Vector   // one least-squares solution
	NullBasis  []*Vector // basis of null(A)
	MinNorm    *Vector   // minimum Euclidean norm solution
}

func (s *Solution) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "rank: %d\n", s.Rank)
	fmt.Fprintf(&sb, "affine solutions: x = %s", s.Particular)
	for i, v := range s.NullBasis {
		fmt.Fprintf(&sb, " + t%d*%s", i+1, v)
	}
	fmt.Fprintf(&sb, "\nmin-norm solution: %s\n", s.MinNorm)
	return sb.String()
}

// LeastSquares solves min ||A x - b||_2 exactly over the rationals via the
// normal equations A^T A x = A^T b (always consistent). Degenerate shapes
// are supported: zero samples (0-by-n A) make every x optimal, and zero
// unknowns (m-by-0 A) yield the empty solution.
func LeastSquares(a *Matrix, b *Vector, budget Budget) (*Solution, error) {
	if a.Rows != b.Len() {
		return nil, ErrDimension
	}
	at := a.Transpose()
	ata, err := at.Mul(a, budget)
	if err != nil {
		return nil, err
	}
	atb, err := at.MulVec(b, budget)
	if err != nil {
		return nil, err
	}
	particular, nullBasis, err := solveLinear(ata, atb, budget)
	if err != nil {
		return nil, err
	}
	minNorm, err := minNormSolution(particular, nullBasis, budget)
	if err != nil {
		return nil, err
	}
	return &Solution{
		Rank:       a.Cols - len(nullBasis),
		Particular: particular,
		NullBasis:  nullBasis,
		MinNorm:    minNorm,
	}, nil
}

// minNormSolution projects the particular solution off the null space:
// x* = x0 - N (N^T N)^{-1} N^T x0, where N holds the null basis as
// columns. All steps are exact rational arithmetic.
func minNormSolution(particular *Vector, nullBasis []*Vector, budget Budget) (*Vector, error) {
	n := particular.Len()
	k := len(nullBasis)
	if k == 0 {
		return particular.Clone(), nil
	}
	nullMat := NewMatrix(n, k)
	for j, v := range nullBasis {
		if v.Len() != n {
			return nil, ErrDimension
		}
		for i := 0; i < n; i++ {
			nullMat.set(i, j, v.At(i))
		}
	}
	nt := nullMat.Transpose()
	gram, err := nt.Mul(nullMat, budget)
	if err != nil {
		return nil, err
	}
	rhs, err := nt.MulVec(particular, budget)
	if err != nil {
		return nil, err
	}
	y, gramNull, err := solveLinear(gram, rhs, budget)
	if err != nil {
		return nil, fmt.Errorf("ratlsq: min-norm projection failed: %v", err)
	}
	if len(gramNull) != 0 {
		return nil, fmt.Errorf("ratlsq: min-norm projection: singular Gram matrix")
	}
	ny, err := nullMat.MulVec(y, budget)
	if err != nil {
		return nil, err
	}
	x := particular.Clone()
	tmp := new(big.Rat)
	for i := 0; i < n; i++ {
		tmp.Sub(x.At(i), ny.At(i))
		x.Set(i, tmp)
	}
	return x, nil
}
