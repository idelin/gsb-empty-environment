package matdiag

// Invert 由 LU 因子逐列求 A 的逆。
// 这是诊断工具：显式求逆为 O(n^3)，仅用于估计条件数，不用于求解。
func Invert(f *Factors) [][]float64 {
	n := len(f.LU)
	inv := make([][]float64, n)
	for i := range inv {
		inv[i] = make([]float64, n)
	}
	for j := 0; j < n; j++ {
		e := make([]float64, n)
		e[j] = 1
		col := f.Solve(e)
		for i := 0; i < n; i++ {
			inv[i][j] = col[i]
		}
	}
	return inv
}

// Cond1 估计 1-范数条件数 ||A||_1 · ||A^{-1}||_1。
// 条件数是尺度无关的：A 乘以任意非零常数后其值不变。
func Cond1(a [][]float64, f *Factors) float64 {
	return OneNorm(a) * OneNorm(Invert(f))
}
