package ratls

import (
	"errors"
	"math/big"
)

// rref computes the reduced row echelon form of m over Q using exact
// Gauss-Jordan elimination. It returns the RREF and the pivot column
// indices (whose count is the rank).
func rref(m *Matrix, bud budget) (r *Matrix, pivots []int, err error) {
	r = m.Copy()
	row := 0
	for col := 0; col < r.Cols && row < r.Rows; col++ {
		piv := -1
		for i := row; i < r.Rows; i++ {
			if r.e(i, col).Sign() != 0 {
				piv = i
				break
			}
		}
		if piv < 0 {
			continue
		}
		r.swapRows(row, piv)
		inv, err := bud.inv(r.e(row, col))
		if err != nil {
			return nil, nil, err
		}
		for j := 0; j < r.Cols; j++ {
			v, err := bud.mul(r.e(row, j), inv)
			if err != nil {
				return nil, nil, err
			}
			r.data[row*r.Cols+j] = v
		}
		for i := 0; i < r.Rows; i++ {
			if i == row {
				continue
			}
			f := r.e(i, col)
			if f.Sign() == 0 {
				continue
			}
			for j := 0; j < r.Cols; j++ {
				p, err := bud.mul(f, r.e(row, j))
				if err != nil {
					return nil, nil, err
				}
				v, err := bud.sub(r.e(i, j), p)
				if err != nil {
					return nil, nil, err
				}
				r.data[i*r.Cols+j] = v
			}
		}
		pivots = append(pivots, col)
		row++
	}
	return r, pivots, nil
}

// nullSpaceFromRREF extracts a basis of the null space of an m x n
// matrix from its RREF. r may carry extra trailing columns (e.g. an
// augmented RHS); only the first n columns are consulted.
func nullSpaceFromRREF(r *Matrix, pivots []int, n int) [][]*big.Rat {
	isPivot := make([]bool, n)
	pivotRow := make([]int, n)
	for i, p := range pivots {
		if p < n {
			isPivot[p] = true
			pivotRow[p] = i
		}
	}
	var basis [][]*big.Rat
	for f := 0; f < n; f++ {
		if isPivot[f] {
			continue
		}
		v := make([]*big.Rat, n)
		for i := range v {
			v[i] = new(big.Rat)
		}
		v[f].SetInt64(1)
		for _, p := range pivots {
			if p >= n {
				continue
			}
			v[p].Neg(r.e(pivotRow[p], f))
		}
		basis = append(basis, v)
	}
	return basis
}

// solveConsistent solves m x = c exactly, where m is n x n and the
// system is assumed consistent. It returns one particular solution
// (free variables set to zero) and a basis of the null space of m, so
// that all solutions are particular + span(basis). It returns an error
// if the system turns out to be inconsistent.
func solveConsistent(m *Matrix, c []*big.Rat, bud budget) (particular []*big.Rat, nullBasis [][]*big.Rat, err error) {
	n := m.Cols
	if m.Rows != n || len(c) != n {
		return nil, nil, errors.New("ratls: solveConsistent expects a square system")
	}
	aug := NewMatrix(n, n+1)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			aug.data[i*aug.Cols+j].Set(m.e(i, j))
		}
		aug.data[i*aug.Cols+n].Set(c[i])
	}
	r, pivots, err := rref(aug, bud)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pivots {
		if p == n {
			return nil, nil, errors.New("ratls: inconsistent linear system")
		}
	}
	particular = make([]*big.Rat, n)
	for i := range particular {
		particular[i] = new(big.Rat)
	}
	for i, p := range pivots {
		particular[p].Set(r.e(i, n))
	}
	nullBasis = nullSpaceFromRREF(r, pivots, n)
	return particular, nullBasis, nil
}
