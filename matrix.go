package erasure

import "errors"

var errSingular = errors.New("erasure: singular matrix")

// gfMatrixInvert inverts a square matrix over GF(256) in place using
// Gauss-Jordan elimination and returns the inverse.
func gfMatrixInvert(m [][]byte) ([][]byte, error) {
	n := len(m)
	inv := make([][]byte, n)
	for i := range inv {
		inv[i] = make([]byte, n)
		inv[i][i] = 1
	}
	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if m[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			return nil, errSingular
		}
		if pivot != col {
			m[col], m[pivot] = m[pivot], m[col]
			inv[col], inv[pivot] = inv[pivot], inv[col]
		}
		scale := gfInv(m[col][col])
		if scale != 1 {
			for c := 0; c < n; c++ {
				m[col][c] = gfMul(m[col][c], scale)
				inv[col][c] = gfMul(inv[col][c], scale)
			}
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			factor := m[r][col]
			if factor == 0 {
				continue
			}
			for c := 0; c < n; c++ {
				m[r][c] ^= gfMul(factor, m[col][c])
				inv[r][c] ^= gfMul(factor, inv[col][c])
			}
		}
	}
	return inv, nil
}
