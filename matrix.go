package erasure

import "errors"

var errSingularMatrix = errors.New("erasure: singular decoding matrix")

// vandermondeRow returns the generator-matrix row for parity shard p
// (0-based): coef[c] = x^c with x = p+1. Distinct non-zero x per parity
// shard makes any k rows of the full (k+m) x k generator matrix
// linearly independent (MDS / Reed-Solomon property).
func vandermondeRow(p, k int) []byte {
	row := make([]byte, k)
	x := byte(p + 1)
	row[0] = 1
	for c := 1; c < k; c++ {
		row[c] = gfMul(row[c-1], x)
	}
	return row
}

// generatorRow returns the generator-matrix row for shard id:
// identity row for data shards, Vandermonde row for parity shards.
func generatorRow(id, k int) []byte {
	if id < k {
		row := make([]byte, k)
		row[id] = 1
		return row
	}
	return vandermondeRow(id-k, k)
}

// invertMatrix inverts a square matrix over GF(256) using
// Gauss-Jordan elimination with partial pivoting.
func invertMatrix(m [][]byte) ([][]byte, error) {
	n := len(m)
	aug := make([][]byte, n)
	for i := range m {
		if len(m[i]) != n {
			return nil, errSingularMatrix
		}
		aug[i] = make([]byte, 2*n)
		copy(aug[i], m[i])
		aug[i][n+i] = 1
	}
	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if aug[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			return nil, errSingularMatrix
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]
		inv := gfInv(aug[col][col])
		for c := 0; c < 2*n; c++ {
			aug[col][c] = gfMul(aug[col][c], inv)
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			if f := aug[r][col]; f != 0 {
				for c := 0; c < 2*n; c++ {
					aug[r][c] ^= gfMul(f, aug[col][c])
				}
			}
		}
	}
	inv := make([][]byte, n)
	for i := range aug {
		inv[i] = aug[i][n:]
	}
	return inv, nil
}
