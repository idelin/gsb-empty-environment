package main

import (
	"errors"
	"math"
)

const lpEps = 1e-9

var (
	errLPInfeasible = errors.New("lp: 无可行解")
	errLPUnbounded  = errors.New("lp: 目标无界")
)

// lpSolver 求解 max c·x, 约束 A·x <= b, x >= 0。
// 两阶段单纯形法，Bland 风格平局规则保证终止。
type lpSolver struct {
	m, n  int
	basis []int
	nonb  []int
	d     [][]float64
}

func newLPSolver(a [][]float64, b, c []float64) *lpSolver {
	m, n := len(b), len(c)
	s := &lpSolver{m: m, n: n, basis: make([]int, m), nonb: make([]int, n+1)}
	s.d = make([][]float64, m+2)
	for i := range s.d {
		s.d[i] = make([]float64, n+2)
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			s.d[i][j] = a[i][j]
		}
	}
	for i := 0; i < m; i++ {
		s.basis[i] = n + i
		s.d[i][n] = -1
		s.d[i][n+1] = b[i]
	}
	for j := 0; j < n; j++ {
		s.nonb[j] = j
		s.d[m][j] = -c[j]
	}
	s.nonb[n] = -1
	s.d[m+1][n] = 1
	return s
}

func (s *lpSolver) pivot(r, c int) {
	d := s.d
	inv := 1.0 / d[r][c]
	for i := 0; i < s.m+2; i++ {
		if i == r {
			continue
		}
		for j := 0; j < s.n+2; j++ {
			if j == c {
				continue
			}
			d[i][j] -= d[r][j] * d[i][c] * inv
		}
	}
	for j := 0; j < s.n+2; j++ {
		if j != c {
			d[r][j] *= inv
		}
	}
	for i := 0; i < s.m+2; i++ {
		if i != r {
			d[i][c] *= -inv
		}
	}
	d[r][c] = inv
	s.basis[r], s.nonb[c] = s.nonb[c], s.basis[r]
}

func (s *lpSolver) simplex(phase int) bool {
	x := s.m
	if phase == 1 {
		x = s.m + 1
	}
	for {
		c := -1
		for j := 0; j <= s.n; j++ {
			if phase == 2 && s.nonb[j] == -1 {
				continue
			}
			if c == -1 || s.d[x][j] < s.d[x][c]-lpEps ||
				(math.Abs(s.d[x][j]-s.d[x][c]) <= lpEps && s.nonb[j] < s.nonb[c]) {
				c = j
			}
		}
		if c == -1 || s.d[x][c] >= -lpEps {
			return true
		}
		r := -1
		for i := 0; i < s.m; i++ {
			if s.d[i][c] <= lpEps {
				continue
			}
			if r == -1 {
				r = i
				continue
			}
			ri := s.d[i][s.n+1] / s.d[i][c]
			rr := s.d[r][s.n+1] / s.d[r][c]
			if ri < rr-lpEps || (math.Abs(ri-rr) <= lpEps && s.basis[i] < s.basis[r]) {
				r = i
			}
		}
		if r == -1 {
			return false
		}
		s.pivot(r, c)
	}
}

// solve 返回 (最优值, 最优解 x, 错误)。
func (s *lpSolver) solve() (float64, []float64, error) {
	if s.m > 0 {
		r := 0
		for i := 1; i < s.m; i++ {
			if s.d[i][s.n+1] < s.d[r][s.n+1] {
				r = i
			}
		}
		if s.d[r][s.n+1] < -lpEps {
			s.pivot(r, s.n)
			if !s.simplex(1) || s.d[s.m+1][s.n+1] < -lpEps {
				return 0, nil, errLPInfeasible
			}
			// 把仍留在基中的人工变量换出
			for i := 0; i < s.m; i++ {
				if s.basis[i] != -1 {
					continue
				}
				c := -1
				for j := 0; j <= s.n; j++ {
					if c == -1 || s.d[i][j] < s.d[i][c]-lpEps ||
						(math.Abs(s.d[i][j]-s.d[i][c]) <= lpEps && s.nonb[j] < s.nonb[c]) {
						c = j
					}
				}
				if c == -1 || math.Abs(s.d[i][c]) <= lpEps {
					continue
				}
				s.pivot(i, c)
			}
		}
	}
	if !s.simplex(2) {
		return 0, nil, errLPUnbounded
	}
	x := make([]float64, s.n)
	for i := 0; i < s.m; i++ {
		if s.basis[i] >= 0 && s.basis[i] < s.n {
			x[s.basis[i]] = s.d[i][s.n+1]
		}
	}
	return s.d[s.m][s.n+1], x, nil
}
