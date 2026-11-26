package admission

import (
	"fmt"
	"math/big"
)

// intMatrix is a dense arbitrary-precision integer matrix.
type intMatrix struct {
	Rows, Cols int
	A          [][]*big.Int
}

func newIntMatrix(rows, cols int) *intMatrix {
	m := &intMatrix{Rows: rows, Cols: cols, A: make([][]*big.Int, rows)}
	for i := range m.A {
		m.A[i] = make([]*big.Int, cols)
		for j := range m.A[i] {
			m.A[i][j] = new(big.Int)
		}
	}
	return m
}

func identityMatrix(n int) *intMatrix {
	m := newIntMatrix(n, n)
	for i := 0; i < n; i++ {
		m.A[i][i].SetInt64(1)
	}
	return m
}

func (m *intMatrix) clone() *intMatrix {
	out := newIntMatrix(m.Rows, m.Cols)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			out.A[i][j].Set(m.A[i][j])
		}
	}
	return out
}

func (m *intMatrix) set(row, col int, z *big.Int) {
	m.A[row][col].Set(z)
}

func (m *intMatrix) rowAdd(dst, src int, scale *big.Int) {
	if scale.Sign() == 0 {
		return
	}
	for j := 0; j < m.Cols; j++ {
		m.A[dst][j].Add(m.A[dst][j], new(big.Int).Mul(m.A[src][j], scale))
	}
}

func (m *intMatrix) colAdd(dst, src int, scale *big.Int) {
	if scale.Sign() == 0 {
		return
	}
	for i := 0; i < m.Rows; i++ {
		m.A[i][dst].Add(m.A[i][dst], new(big.Int).Mul(m.A[i][src], scale))
	}
}

func (m *intMatrix) swapRows(i, j int) {
	if i == j {
		return
	}
	m.A[i], m.A[j] = m.A[j], m.A[i]
}

func (m *intMatrix) swapCols(i, j int) {
	if i == j {
		return
	}
	for r := 0; r < m.Rows; r++ {
		m.A[r][i], m.A[r][j] = m.A[r][j], m.A[r][i]
	}
}

func (m *intMatrix) negateRow(i int) {
	for j := 0; j < m.Cols; j++ {
		m.A[i][j].Neg(m.A[i][j])
	}
}

func matMul(a, b *intMatrix) *intMatrix {
	if a.Cols != b.Rows {
		panic("admission: incompatible matrix dimensions")
	}
	out := newIntMatrix(a.Rows, b.Cols)
	for i := 0; i < a.Rows; i++ {
		for j := 0; j < b.Cols; j++ {
			z := new(big.Int)
			for k := 0; k < a.Cols; k++ {
				z.Add(z, new(big.Int).Mul(a.A[i][k], b.A[k][j]))
			}
			out.A[i][j].Set(z)
		}
	}
	return out
}

// smithNormalForm returns D, U, V with U*A*V = D, U and V unimodular, and D
// diagonal with non-negative invariant factors satisfying d_i | d_{i+1}.
//
// The diagonal entries are the canonical Smith invariants of A.  They do not
// depend on the order in which rows/columns are processed.
func smithNormalForm(a *intMatrix) (d, u, v *intMatrix, err error) {
	if a == nil {
		return nil, nil, nil, fmt.Errorf("admission: nil matrix")
	}
	work := a.clone()
	u = identityMatrix(a.Rows)
	v = identityMatrix(a.Cols)
	limit := maxInt(1024, a.Rows*a.Cols*256)

	k := 0
	for k < a.Rows && k < a.Cols {
		if submatrixZero(work, k) {
			break
		}
		if work.A[k][k].Sign() == 0 {
			found := false
			for i := k; i < work.Rows && !found; i++ {
				for j := k; j < work.Cols; j++ {
					if work.A[i][j].Sign() != 0 {
						work.swapRows(k, i)
						u.swapRows(k, i)
						work.swapCols(k, j)
						v.swapCols(k, j)
						found = true
						break
					}
				}
			}
			if !found {
				break
			}
		}

		iterations := 0
		for {
			iterations++
			if iterations > limit {
				return nil, nil, nil, fmt.Errorf("admission: Smith normal form did not converge")
			}
			reducePivotColumn(work, u, k)
			reducePivotRow(work, v, k)
			if !columnTailZero(work, k) || !rowTailZero(work, k) {
				continue
			}
			badI, _ := firstNonDivisible(work, k)
			if badI >= 0 {
				// Adding the offending row to the pivot row keeps the pivot
				// unchanged (the offending row has zero in column k) and
				// introduces an entry not divisible by the pivot.  The next
				// reduction replaces the pivot by a smaller gcd.
				work.rowAdd(k, badI, big.NewInt(1))
				u.rowAdd(k, badI, big.NewInt(1))
				continue
			}
			break
		}
		if work.A[k][k].Sign() < 0 {
			work.negateRow(k)
			u.negateRow(k)
		}
		k++
	}

	// Clear round-off-free off-diagonal noise outside the processed block.
	for i := 0; i < a.Rows; i++ {
		for j := 0; j < a.Cols; j++ {
			if i != j && (i >= k || j >= k) {
				work.A[i][j].SetInt64(0)
			}
		}
	}
	return work, u, v, nil
}

func submatrixZero(m *intMatrix, k int) bool {
	for i := k; i < m.Rows; i++ {
		for j := k; j < m.Cols; j++ {
			if m.A[i][j].Sign() != 0 {
				return false
			}
		}
	}
	return true
}

func columnTailZero(m *intMatrix, k int) bool {
	for i := k + 1; i < m.Rows; i++ {
		if m.A[i][k].Sign() != 0 {
			return false
		}
	}
	return true
}

func rowTailZero(m *intMatrix, k int) bool {
	for j := k + 1; j < m.Cols; j++ {
		if m.A[k][j].Sign() != 0 {
			return false
		}
	}
	return true
}

func firstNonDivisible(m *intMatrix, k int) (int, int) {
	p := m.A[k][k]
	if p.Sign() == 0 {
		return -1, -1
	}
	for i := k + 1; i < m.Rows; i++ {
		for j := k + 1; j < m.Cols; j++ {
			if new(big.Int).Mod(m.A[i][j], p).Sign() != 0 {
				return i, j
			}
		}
	}
	return -1, -1
}

// reducePivotColumn uses Euclidean row operations to make all entries below
// (k,k) zero.  Rows are swapped whenever a smaller remainder appears.
func reducePivotColumn(m, u *intMatrix, k int) {
	for {
		if m.A[k][k].Sign() == 0 {
			found := false
			for i := k + 1; i < m.Rows; i++ {
				if m.A[i][k].Sign() != 0 {
					m.swapRows(k, i)
					u.swapRows(k, i)
					found = true
					break
				}
			}
			if !found {
				return
			}
			continue
		}
		swapped := false
		for i := k + 1; i < m.Rows; i++ {
			if m.A[i][k].Sign() == 0 {
				continue
			}
			q := new(big.Int)
			r := new(big.Int)
			q.QuoRem(m.A[i][k], m.A[k][k], r)
			if q.Sign() != 0 {
				scale := new(big.Int).Neg(q)
				m.rowAdd(i, k, scale)
				u.rowAdd(i, k, scale)
			}
			if r.Sign() != 0 {
				m.swapRows(k, i)
				u.swapRows(k, i)
				swapped = true
				break
			}
		}
		if !swapped {
			return
		}
	}
}

// reducePivotRow is the column analogue of reducePivotColumn.
func reducePivotRow(m, v *intMatrix, k int) {
	for {
		if m.A[k][k].Sign() == 0 {
			found := false
			for j := k + 1; j < m.Cols; j++ {
				if m.A[k][j].Sign() != 0 {
					m.swapCols(k, j)
					v.swapCols(k, j)
					found = true
					break
				}
			}
			if !found {
				return
			}
			continue
		}
		swapped := false
		for j := k + 1; j < m.Cols; j++ {
			if m.A[k][j].Sign() == 0 {
				continue
			}
			q := new(big.Int)
			r := new(big.Int)
			q.QuoRem(m.A[k][j], m.A[k][k], r)
			if q.Sign() != 0 {
				scale := new(big.Int).Neg(q)
				m.colAdd(j, k, scale)
				v.colAdd(j, k, scale)
			}
			if r.Sign() != 0 {
				m.swapCols(k, j)
				v.swapCols(k, j)
				swapped = true
				break
			}
		}
		if !swapped {
			return
		}
	}
}
