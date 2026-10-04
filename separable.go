package conv

import "math/big"

// separate decomposes k into a column kernel (kh x 1) and a row kernel
// (1 x kw) such that K[y][x] == col[y][0]*row[0][x] exactly, reporting
// ok=true on success. It returns ok=false when k is not exactly separable
// over the integers.
//
// The decomposition is exact: rank-1 verification multiplies coefficients
// with math/big, so no magnitude of int64 coefficients can cause a false
// positive through overflow.
func (k Kernel) separate() (col, row [][]int64, ok bool) {
	kh := len(k.Coeffs)
	kw := len(k.Coeffs[0])

	col = make([][]int64, kh)
	for i := range col {
		col[i] = make([]int64, 1)
	}
	row = make([][]int64, 1)
	row[0] = make([]int64, kw)

	// Locate the first non-zero coefficient as the pivot.
	i0, j0 := -1, -1
	for i := 0; i < kh && i0 < 0; i++ {
		for j := 0; j < kw; j++ {
			if k.Coeffs[i][j] != 0 {
				i0, j0 = i, j
				break
			}
		}
	}
	if i0 < 0 {
		// The zero kernel is separable into zero factors.
		return col, row, true
	}

	// Rank-1 check: every 2x2 minor through the pivot must vanish, i.e.
	// K[i][j]*K[i0][j0] == K[i][j0]*K[i0][j] for all i, j.
	pivot := big.NewInt(k.Coeffs[i0][j0])
	lhs, rhs := new(big.Int), new(big.Int)
	for i := 0; i < kh; i++ {
		ki0 := big.NewInt(k.Coeffs[i][j0])
		for j := 0; j < kw; j++ {
			lhs.Mul(big.NewInt(k.Coeffs[i][j]), pivot)
			rhs.Mul(ki0, big.NewInt(k.Coeffs[i0][j]))
			if lhs.Cmp(rhs) != 0 {
				return nil, nil, false
			}
		}
	}

	// The kernel is exactly rank 1, so K[i][j] = col[i]*row[j] with
	// col[i] = K[i][j0]/g and row[j] = K[i0][j]/col[i0], where g is the
	// gcd of pivot column j0. Both divisions are exact and the results
	// fit int64: |col[i]| <= |K[i][j0]| and |row[j]| <= |K[i0][j]|.
	g := new(big.Int)
	v, gg := new(big.Int), new(big.Int)
	for i := 0; i < kh; i++ {
		v.Abs(big.NewInt(k.Coeffs[i][j0]))
		if v.Sign() == 0 {
			continue
		}
		if g.Sign() == 0 {
			g.Set(v)
			continue
		}
		gg.GCD(nil, nil, g, v)
		g.Set(gg)
	}
	// g > 0 because the pivot column contains the non-zero pivot.
	tmp := new(big.Int)
	for i := 0; i < kh; i++ {
		col[i][0] = tmp.Quo(big.NewInt(k.Coeffs[i][j0]), g).Int64()
	}
	colPivot := big.NewInt(col[i0][0]) // non-zero by construction
	for j := 0; j < kw; j++ {
		row[0][j] = tmp.Quo(big.NewInt(k.Coeffs[i0][j]), colPivot).Int64()
	}
	return col, row, true
}
