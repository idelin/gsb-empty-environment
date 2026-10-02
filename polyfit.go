package kinematics

import "math"

// polyFit 对 (xs, ys) 做最小二乘多项式拟合，返回系数 c，
// 使得 y ≈ c[0] + c[1]*x + ... + c[degree]*x^degree。
// 通过正规方程 + 高斯消元（部分主元）求解，仅依赖标准库。
func polyFit(xs, ys []float64, degree int) []float64 {
	n := degree + 1
	ata := make([][]float64, n)
	for i := range ata {
		ata[i] = make([]float64, n)
	}
	atb := make([]float64, n)

	// 预计算每个样本的 x 的幂次
	powers := make([][]float64, len(xs))
	for i, x := range xs {
		row := make([]float64, 2*degree+1)
		row[0] = 1
		for p := 1; p <= 2*degree; p++ {
			row[p] = row[p-1] * x
		}
		powers[i] = row
	}

	for i := range xs {
		p := powers[i]
		for r := 0; r < n; r++ {
			atb[r] += p[r] * ys[i]
			for c := 0; c < n; c++ {
				ata[r][c] += p[r+c]
			}
		}
	}
	return solveLinear(ata, atb)
}

// solveLinear 用部分主元高斯消元求解 A x = b。
func solveLinear(a [][]float64, b []float64) []float64 {
	n := len(b)
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		a[col], a[piv] = a[piv], a[col]
		b[col], b[piv] = b[piv], b[col]
		for r := col + 1; r < n; r++ {
			f := a[r][col] / a[col][col]
			for c := col; c < n; c++ {
				a[r][c] -= f * a[col][c]
			}
			b[r] -= f * b[col]
		}
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := b[i]
		for j := i + 1; j < n; j++ {
			s -= a[i][j] * x[j]
		}
		x[i] = s / a[i][i]
	}
	return x
}
