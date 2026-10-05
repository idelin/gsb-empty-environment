package matdiag

import (
	"errors"
	"math"
	"math/big"
)

// RatMatrix 把字符串矩阵解析为精确有理数矩阵。
// 每个元素可为 "p/q"、"0.563"、"1e-12" 等形式。
func RatMatrix(rows ...[]string) [][]*big.Rat {
	m := make([][]*big.Rat, len(rows))
	for i, row := range rows {
		m[i] = make([]*big.Rat, len(row))
		for j, s := range row {
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				panic("matdiag: 无法解析有理数 " + s)
			}
			m[i][j] = r
		}
	}
	return m
}

// RatVector 把字符串切片解析为精确有理数向量。
func RatVector(entries ...string) []*big.Rat {
	v := make([]*big.Rat, len(entries))
	for i, s := range entries {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic("matdiag: 无法解析有理数 " + s)
		}
		v[i] = r
	}
	return v
}

// ExactSolve 用精确有理数高斯消元解 A x = b，作为浮点解的参照真值。
func ExactSolve(a [][]*big.Rat, b []*big.Rat) ([]*big.Rat, error) {
	n := len(a)
	m := make([][]*big.Rat, n)
	for i := range m {
		m[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			m[i][j] = new(big.Rat).Set(a[i][j])
		}
		m[i][n] = new(big.Rat).Set(b[i])
	}
	for k := 0; k < n; k++ {
		p := -1
		for i := k; i < n; i++ {
			if m[i][k].Sign() != 0 {
				p = i
				break
			}
		}
		if p < 0 {
			return nil, errors.New("matdiag: 矩阵精确奇异")
		}
		m[k], m[p] = m[p], m[k]
		for i := k + 1; i < n; i++ {
			factor := new(big.Rat).Quo(m[i][k], m[k][k])
			for j := k; j <= n; j++ {
				m[i][j].Sub(m[i][j], new(big.Rat).Mul(factor, m[k][j]))
			}
		}
	}
	x := make([]*big.Rat, n)
	for i := n - 1; i >= 0; i-- {
		s := new(big.Rat).Set(m[i][n])
		for j := i + 1; j < n; j++ {
			s.Sub(s, new(big.Rat).Mul(m[i][j], x[j]))
		}
		x[i] = s.Quo(s, m[i][i])
	}
	return x, nil
}

// FloatMatrix 把有理矩阵转为 float64（用于把参照问题送入浮点求解器）。
func FloatMatrix(a [][]*big.Rat) [][]float64 {
	m := make([][]float64, len(a))
	for i, row := range a {
		m[i] = make([]float64, len(row))
		for j, r := range row {
			m[i][j], _ = r.Float64()
		}
	}
	return m
}

// FloatVector 把有理向量转为 float64。
func FloatVector(v []*big.Rat) []float64 {
	out := make([]float64, len(v))
	for i, r := range v {
		out[i], _ = r.Float64()
	}
	return out
}

// RelForwardError 返回 ||x - x_exact||_inf / ||x_exact||_inf，
// 即相对前向误差——这是残差/后向误差无法直接反映的量。
func RelForwardError(x []float64, exact []*big.Rat) float64 {
	num, den := 0.0, 0.0
	for i := range x {
		e, _ := exact[i].Float64()
		if d := math.Abs(x[i] - e); d > num {
			num = d
		}
		if a := math.Abs(e); a > den {
			den = a
		}
	}
	if den == 0 {
		return num
	}
	return num / den
}
