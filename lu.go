package matdiag

import "math"

// Factors 保存带部分主元 LU 分解的结果与主元诊断信息。
//
// 分解满足 PA = LU，其中 P 由 Perm 表示：置换后第 i 行是原矩阵第 Perm[i] 行。
type Factors struct {
	LU   [][]float64 // 下三角存 L（对角线隐含为 1），上三角存 U
	Perm []int       // 行置换
	// PivotRatios[k] 是第 k 步主元绝对值与整个矩阵最大元绝对值之比。
	// 该比值对矩阵整体乘以常数保持不变，因此是尺度无关的。
	PivotRatios []float64
	// Singular 为真表示消元中遇到精确零主元（整列候选皆为 0），
	// 即矩阵在精确意义下秩亏，而不是“小于某个绝对阈值”。
	Singular   bool
	SingularAt int // 发现秩亏的消元步；未奇异时为 -1
}

// Factor 对 n×n 矩阵 a 做带部分主元的 LU 分解。a 不被修改。
func Factor(a [][]float64) *Factors {
	n := len(a)
	lu := copyMatrix(a)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	f := &Factors{LU: lu, Perm: perm, PivotRatios: make([]float64, n), SingularAt: -1}

	scale := 0.0
	for _, row := range a {
		for _, v := range row {
			if math.Abs(v) > scale {
				scale = math.Abs(v)
			}
		}
	}

	for k := 0; k < n; k++ {
		p := k
		for i := k + 1; i < n; i++ {
			if math.Abs(lu[i][k]) > math.Abs(lu[p][k]) {
				p = i
			}
		}
		piv := math.Abs(lu[p][k])
		if scale > 0 {
			f.PivotRatios[k] = piv / scale
		}
		if piv == 0 {
			// 当前列第 k..n-1 行全为 0：精确秩亏。
			f.Singular = true
			f.SingularAt = k
			return f
		}
		if p != k {
			lu[p], lu[k] = lu[k], lu[p]
			perm[p], perm[k] = perm[k], perm[p]
		}
		for i := k + 1; i < n; i++ {
			lu[i][k] /= lu[k][k]
			for j := k + 1; j < n; j++ {
				lu[i][j] -= lu[i][k] * lu[k][j]
			}
		}
	}
	return f
}

// MinPivotRatio 返回各消元步中最小的尺度无关主元比。
func (f *Factors) MinPivotRatio() float64 {
	m := math.Inf(1)
	for _, r := range f.PivotRatios {
		if r < m {
			m = r
		}
	}
	return m
}

// Solve 用已分解的因子解 A x = b，返回新切片。
func (f *Factors) Solve(b []float64) []float64 {
	n := len(f.LU)
	x := make([]float64, n)
	for i := range x {
		x[i] = b[f.Perm[i]]
	}
	// 前代：解 L y = P b
	for i := 1; i < n; i++ {
		for j := 0; j < i; j++ {
			x[i] -= f.LU[i][j] * x[j]
		}
	}
	// 回代：解 U x = y
	for i := n - 1; i >= 0; i-- {
		for j := i + 1; j < n; j++ {
			x[i] -= f.LU[i][j] * x[j]
		}
		x[i] /= f.LU[i][i]
	}
	return x
}
