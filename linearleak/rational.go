package linearleak

import (
	"math/big"
)

// ratRow 表示一条增广有理行：[系数 | 右端项]。
type ratRow []*big.Rat

// ratMatrix 按 subset 把整数方程组转成有理数增广矩阵。
func (an *analyzer) ratMatrix(subset []int) []ratRow {
	mat := make([]ratRow, 0, len(subset))
	for _, idx := range subset {
		row := make(ratRow, an.n+1)
		for j := 0; j < an.n; j++ {
			row[j] = big.NewRat(an.coeff[idx][j], 1)
		}
		row[an.n] = big.NewRat(an.rhs[idx], 1)
		mat = append(mat, row)
	}
	return mat
}

// rref 对增广矩阵做高斯-若尔当消元。
//
// 返回主元列 pivots、自由变量列 frees，以及 inconsistent。
// inconsistent 为 true 表示存在 0 = 非零 的行，即实数域（因而布尔域）无解。
func rref(mat []ratRow) (pivots, frees []int, inconsistent bool) {
	if len(mat) == 0 {
		frees = make([]int, 0)
		return pivots, frees, false
	}
	n := len(mat[0]) - 1
	rank := 0

	for col := 0; col < n && rank < len(mat); col++ {
		pivot := -1
		for r := rank; r < len(mat); r++ {
			if mat[r][col].Sign() != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			continue
		}
		mat[rank], mat[pivot] = mat[pivot], mat[rank]

		lead := new(big.Rat).Set(mat[rank][col])
		for j := col; j <= n; j++ {
			mat[rank][j].Quo(mat[rank][j], lead)
		}
		for r := 0; r < len(mat); r++ {
			if r == rank || mat[r][col].Sign() == 0 {
				continue
			}
			factor := new(big.Rat).Set(mat[r][col])
			for j := col; j <= n; j++ {
				mat[r][j].Sub(mat[r][j], new(big.Rat).Mul(factor, mat[rank][j]))
			}
		}
		pivots = append(pivots, col)
		rank++
	}

	pivotSet := make(map[int]bool, len(pivots))
	for _, p := range pivots {
		pivotSet[p] = true
	}
	for j := 0; j < n; j++ {
		if !pivotSet[j] {
			frees = append(frees, j)
		}
	}

	for r := rank; r < len(mat); r++ {
		allZero := true
		for j := 0; j < n; j++ {
			if mat[r][j].Sign() != 0 {
				allZero = false
				break
			}
		}
		if allZero && mat[r][n].Sign() != 0 {
			return pivots, frees, true
		}
	}
	return pivots, frees, false
}

// realFixed 利用 RREF 给出实数域上被唯一固定的变量。
//
// 主元列变量的行中若不含任何自由变量系数，则该变量恒等于常数，
// 即其在 null space 的每个基向量上系数为零。返回 fixed 变量列与
// 对应的固定有理值。
func realFixed(mat []ratRow, pivots, frees []int) (fixed []int, values []*big.Rat) {
	if len(mat) == 0 {
		return nil, nil
	}
	n := len(mat[0]) - 1
	freeSet := make(map[int]bool, len(frees))
	for _, f := range frees {
		freeSet[f] = true
	}

	// RREF 中前 len(pivots) 行依次承载主元。
	for r, p := range pivots {
		dependsOnFree := false
		for j := 0; j < n; j++ {
			if j != p && freeSet[j] && mat[r][j].Sign() != 0 {
				dependsOnFree = true
				break
			}
		}
		if !dependsOnFree {
			fixed = append(fixed, p)
			values = append(values, new(big.Rat).Set(mat[r][n]))
		}
	}
	return fixed, values
}

// ratBit 在有理值恰为 0 或 1 时返回对应布尔值。
func ratBit(v *big.Rat) (int, bool) {
	if v.IsInt() {
		num := v.Num()
		if num.Cmp(big.NewInt(0)) == 0 {
			return 0, true
		}
		if num.Cmp(big.NewInt(1)) == 0 {
			return 1, true
		}
	}
	return 0, false
}
