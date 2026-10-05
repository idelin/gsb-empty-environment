package matdiag

import "math"

// Scaling 描述对角均衡：Dr = diag(Row)，Dc = diag(Col)。
//
// 原系统 A x = b 经缩放后变为
//
//	(Dr·A·Dc) y = Dr·b
//
// 映射回原变量的方式为
//
//	x = Dc·y   （即 x[i] = Col[i] * y[i]）
//
// 行缩放 Dr 只作用于系数矩阵和右端项，不改变未知量；
// 列缩放 Dc 等价于换未知量 y = Dc^{-1} x，因此解出 y 后必须乘回 Dc。
// 缩放因子取 2 的幂，乘以 2 的幂在二进制浮点下是精确的，不引入额外舍入。
type Scaling struct {
	Row []float64
	Col []float64
}

// roundPow2 把正的缩放目标舍入到最近的 2 的幂方向（使缩放后最大值落在 [0.5, 1)）。
func pow2Scale(maxAbs float64) float64 {
	if maxAbs <= 0 {
		return 1
	}
	_, e := math.Frexp(maxAbs) // maxAbs = f * 2^e, 0.5 <= f < 1
	return math.Ldexp(1, -e)
}

// Equilibrate 计算行/列对角均衡：先按行最大元缩放，再按列最大元缩放。
func Equilibrate(a [][]float64) Scaling {
	n := len(a)
	m := len(a[0])
	s := Scaling{Row: make([]float64, n), Col: make([]float64, m)}
	for i := 0; i < n; i++ {
		max := 0.0
		for _, v := range a[i] {
			if math.Abs(v) > max {
				max = math.Abs(v)
			}
		}
		s.Row[i] = pow2Scale(max)
	}
	for j := 0; j < m; j++ {
		max := 0.0
		for i := 0; i < n; i++ {
			if v := math.Abs(a[i][j]) * s.Row[i]; v > max {
				max = v
			}
		}
		s.Col[j] = pow2Scale(max)
	}
	return s
}

// Apply 返回缩放后的矩阵 Dr·A·Dc 与右端项 Dr·b。
func (s Scaling) Apply(a [][]float64, b []float64) ([][]float64, []float64) {
	n := len(a)
	m := len(a[0])
	as := make([][]float64, n)
	for i := 0; i < n; i++ {
		as[i] = make([]float64, m)
		for j := 0; j < m; j++ {
			as[i][j] = s.Row[i] * a[i][j] * s.Col[j]
		}
	}
	bs := make([]float64, n)
	for i := 0; i < n; i++ {
		bs[i] = s.Row[i] * b[i]
	}
	return as, bs
}

// MapBack 把缩放系统的解 y 映射回原变量：x = Dc·y。
func (s Scaling) MapBack(y []float64) []float64 {
	x := make([]float64, len(y))
	for i := range y {
		x[i] = s.Col[i] * y[i]
	}
	return x
}
