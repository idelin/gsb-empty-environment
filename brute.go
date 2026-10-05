package ilse

import "math/big"

// Enumerate returns all integer solutions x of A x = b with every coordinate
// in [-bound, bound]. It is an exponential-time cross-check meant for small
// coefficient matrices; maxResults caps the output (0 = no cap).
func Enumerate(A *Mat, b []*big.Int, bound int64, maxResults int) [][]*big.Int {
	n := A.Cols
	var out [][]*big.Int
	x := make([]*big.Int, n)
	for i := range x {
		x[i] = new(big.Int)
	}
	// partial[k][i] = sum over the first k assigned columns of A[i][j]*x[j].
	partial := make([][]*big.Int, n+1)
	for k := 0; k <= n; k++ {
		partial[k] = make([]*big.Int, A.Rows)
		for i := range partial[k] {
			partial[k][i] = new(big.Int)
		}
	}
	t := new(big.Int)
	var rec func(k int) bool // returns true to abort early
	rec = func(k int) bool {
		if k == n {
			for i := 0; i < A.Rows; i++ {
				if partial[n][i].Cmp(b[i]) != 0 {
					return false
				}
			}
			sol := make([]*big.Int, n)
			for i := range sol {
				sol[i] = new(big.Int).Set(x[i])
			}
			out = append(out, sol)
			return maxResults > 0 && len(out) >= maxResults
		}
		for v := -bound; v <= bound; v++ {
			x[k].SetInt64(v)
			for i := 0; i < A.Rows; i++ {
				t.Mul(A.At(i, k), x[k])
				partial[k+1][i].Add(partial[k][i], t)
			}
			if rec(k + 1) {
				return true
			}
		}
		return false
	}
	rec(0)
	return out
}
