// Package matdiag 是一个小型线性方程组数值诊断工具。
//
// 功能：带部分主元的 LU 求解、残差与后向误差估计、
// 区分“真奇异”（精确秩亏）与“近奇异”（病态，按尺度无关的
// 相对判据），支持对角均衡缩放并给出映射回原变量的方式，
// 以及基于 big.Rat 的有理精确解作为前向误差参照。
package matdiag

import "math"

// machineEps 是 float64 的机器精度 2^-52 ≈ 2.22e-16。
const machineEps = 1.0 / (1 << 52)

func copyMatrix(a [][]float64) [][]float64 {
	c := make([][]float64, len(a))
	for i, row := range a {
		c[i] = make([]float64, len(row))
		copy(c[i], row)
	}
	return c
}

// InfNorm 返回矩阵的无穷范数（最大行绝对和）。
func InfNorm(a [][]float64) float64 {
	max := 0.0
	for _, row := range a {
		s := 0.0
		for _, v := range row {
			s += math.Abs(v)
		}
		if s > max {
			max = s
		}
	}
	return max
}

// OneNorm 返回矩阵的 1-范数（最大列绝对和）。
func OneNorm(a [][]float64) float64 {
	if len(a) == 0 {
		return 0
	}
	max := 0.0
	for j := 0; j < len(a[0]); j++ {
		s := 0.0
		for i := range a {
			s += math.Abs(a[i][j])
		}
		if s > max {
			max = s
		}
	}
	return max
}

func vecInfNorm(x []float64) float64 {
	max := 0.0
	for _, v := range x {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}

func matVec(a [][]float64, x []float64) []float64 {
	y := make([]float64, len(a))
	for i, row := range a {
		for j, v := range row {
			y[i] += v * x[j]
		}
	}
	return y
}
