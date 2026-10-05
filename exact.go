package main

import "math/big"

// ExactSolve solves A*x = b exactly over the rationals (the float64 inputs
// are converted to their exact binary rational values). It returns the
// exact solution and false if A is exactly singular. This serves as the
// reference for measuring the true forward error of the float64 solver.
func ExactSolve(a [][]float64, b []float64) ([]*big.Rat, bool) {
	n := len(a)
	m := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		m[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			m[i][j] = new(big.Rat).SetFloat64(a[i][j])
			if m[i][j] == nil {
				m[i][j] = new(big.Rat)
			}
		}
		m[i][n] = new(big.Rat).SetFloat64(b[i])
		if m[i][n] == nil {
			m[i][n] = new(big.Rat)
		}
	}
	for k := 0; k < n; k++ {
		p := k
		for i := k + 1; i < n; i++ {
			ai := new(big.Rat).Abs(m[i][k])
			ap := new(big.Rat).Abs(m[p][k])
			if ai.Cmp(ap) > 0 {
				p = i
			}
		}
		if m[p][k].Sign() == 0 {
			return nil, false
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
		x[i] = new(big.Rat).Set(m[i][n])
		for j := i + 1; j < n; j++ {
			x[i].Sub(x[i], new(big.Rat).Mul(m[i][j], x[j]))
		}
		x[i].Quo(x[i], m[i][i])
	}
	return x, true
}

// ExactMatVec computes A*x exactly over the rationals and returns the
// result rounded to float64. Used to manufacture right-hand sides whose
// exact solution is known (e.g. x = ones).
func ExactMatVec(a [][]float64, x []float64) []float64 {
	n := len(a)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		acc := new(big.Rat)
		for j := 0; j < n; j++ {
			aj := new(big.Rat).SetFloat64(a[i][j])
			xj := new(big.Rat).SetFloat64(x[j])
			if aj == nil || xj == nil {
				continue
			}
			acc.Add(acc, new(big.Rat).Mul(aj, xj))
		}
		b[i], _ = acc.Float64()
	}
	return b
}

// RelErrorInf returns ||x - xExact||_inf / ||xExact||_inf.
func RelErrorInf(x []float64, xExact []*big.Rat) float64 {
	num, den := 0.0, 0.0
	for i := range x {
		e, _ := xExact[i].Float64()
		if d := abs(e - x[i]); d > num {
			num = d
		}
		if a := abs(e); a > den {
			den = a
		}
	}
	if den == 0 {
		return num
	}
	return num / den
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
