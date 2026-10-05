package exactmatrix

import "math/big"

// 本文件提供“可靠路径”：全量精确消元。
// 所有零判定都基于 big.Rat.Sign()，是精确相等，绝不使用浮点近零容差。

// rref 对行做精确 Gauss-Jordan 消元（就地），只在前 ncols 列中寻找主元，
// 返回主元列下标（长度即秩）。行会被归一化为主元 1，并消去其它行。
func rref(m [][]big.Rat, ncols int) []int {
	rows := len(m)
	if rows == 0 {
		return nil
	}
	width := len(m[0])
	pivots := []int{}
	r := 0
	for c := 0; c < ncols && r < rows; c++ {
		p := -1
		for i := r; i < rows; i++ {
			if m[i][c].Sign() != 0 {
				p = i
				break
			}
		}
		if p < 0 {
			continue
		}
		m[r], m[p] = m[p], m[r]
		inv := new(big.Rat).Inv(&m[r][c])
		for j := c; j < width; j++ {
			m[r][j].Mul(&m[r][j], inv)
		}
		for i := 0; i < rows; i++ {
			if i == r || m[i][c].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(&m[i][c])
			for j := c; j < width; j++ {
				m[i][j].Sub(&m[i][j], new(big.Rat).Mul(f, &m[r][j]))
			}
		}
		pivots = append(pivots, c)
		r++
	}
	return pivots
}

// detElim 用精确高斯消元计算方阵行列式（不修改输入）。
func detElim(a [][]big.Rat) big.Rat {
	n := len(a)
	m := cloneRows(a)
	det := big.NewRat(1, 1)
	sign := int64(1)
	r := 0
	for c := 0; c < n && r < n; c++ {
		p := -1
		for i := r; i < n; i++ {
			if m[i][c].Sign() != 0 {
				p = i
				break
			}
		}
		if p < 0 {
			return *big.NewRat(0, 1)
		}
		if p != r {
			m[r], m[p] = m[p], m[r]
			sign = -sign
		}
		piv := new(big.Rat).Set(&m[r][c])
		det.Mul(det, piv)
		for i := r + 1; i < n; i++ {
			if m[i][c].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(&m[i][c], piv)
			for j := c; j < n; j++ {
				m[i][j].Sub(&m[i][j], new(big.Rat).Mul(f, &m[r][j]))
			}
		}
		r++
	}
	if r < n {
		return *big.NewRat(0, 1)
	}
	det.Mul(det, big.NewRat(sign, 1))
	return *det
}

// inverseElim 用全量 Gauss-Jordan 求逆；奇异时返回 nil。
func inverseElim(a [][]big.Rat) [][]big.Rat {
	n := len(a)
	aug := make([][]big.Rat, n)
	for i := range aug {
		aug[i] = make([]big.Rat, 2*n)
		for j := 0; j < n; j++ {
			aug[i][j].Set(&a[i][j])
		}
		aug[i][n+i].SetInt64(1)
	}
	pivots := rref(aug, n)
	if len(pivots) < n {
		return nil
	}
	inv := make([][]big.Rat, n)
	for i := range inv {
		inv[i] = make([]big.Rat, n)
		copy(inv[i], aug[i][n:])
	}
	return inv
}

// nullSpace 由已消元为 RREF 的方阵（pivots 为主元列）求零空间一组基。
// 每个自由变量给出一个基向量：该自由分量取 1，主元分量取对应系数的相反数。
// 这些基向量天然线性无关（各自独占一个为 1 的自由分量）。
func nullSpace(rr [][]big.Rat, pivots []int, n int) [][]big.Rat {
	inPivot := make([]bool, n)
	for _, c := range pivots {
		inPivot[c] = true
	}
	basis := [][]big.Rat{}
	for f := 0; f < n; f++ {
		if inPivot[f] {
			continue
		}
		v := make([]big.Rat, n)
		v[f].SetInt64(1)
		for k, c := range pivots {
			v[c].Neg(&rr[k][f])
		}
		basis = append(basis, v)
	}
	return basis
}

func cloneRows(a [][]big.Rat) [][]big.Rat {
	m := make([][]big.Rat, len(a))
	for i := range a {
		m[i] = make([]big.Rat, len(a[i]))
		for j := range a[i] {
			m[i][j].Set(&a[i][j])
		}
	}
	return m
}
