package ils

import "math/big"

// echelon computes an integer row-echelon form of m using only unimodular
// row operations (row swaps, row negation, and adding an integer multiple of
// one row to another). It returns R = V*m together with the unimodular
// matrix V and the list of pivot column indices.
//
// The top len(pivots) rows of R have full row rank; the remaining rows are
// zero. Each pivot entry R[j][pivots[j]] is positive, and R[j][c] = 0 for
// c < pivots[j]. Because every operation is a gcd-style integer elimination,
// no fractions ever appear and big.Int arithmetic cannot overflow.
func echelon(m *Matrix) (R, V *Matrix, pivots []int) {
	R = m.Copy()
	V = Identity(m.Rows)
	r := 0
	for col := 0; col < m.Cols && r < m.Rows; col++ {
		// Euclidean reduction of the column entries in rows >= r until at
		// most one nonzero entry (the pivot) remains.
		for {
			piv := -1
			for i := r; i < R.Rows; i++ {
				if R.At(i, col).Sign() == 0 {
					continue
				}
				if piv == -1 || R.At(i, col).CmpAbs(R.At(piv, col)) < 0 {
					piv = i
				}
			}
			if piv == -1 {
				break // whole column (rows >= r) is zero
			}
			R.SwapRows(piv, r)
			V.SwapRows(piv, r)
			q := new(big.Int)
			for i := r + 1; i < R.Rows; i++ {
				if R.At(i, col).Sign() == 0 {
					continue
				}
				q.Quo(R.At(i, col), R.At(r, col))
				qs := new(big.Int).Neg(q)
				R.AddScaledRow(i, r, qs)
				V.AddScaledRow(i, r, qs)
			}
			done := true
			for i := r + 1; i < R.Rows; i++ {
				if R.At(i, col).Sign() != 0 {
					done = false
					break
				}
			}
			if done {
				break
			}
		}
		if R.At(r, col).Sign() != 0 {
			if R.At(r, col).Sign() < 0 {
				R.NegRow(r)
				V.NegRow(r)
			}
			pivots = append(pivots, col)
			r++
		}
	}
	return R, V, pivots
}
