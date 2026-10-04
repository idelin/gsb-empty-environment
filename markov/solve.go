package main

import "math/big"

// solveLinear 在有理数域上精确求解 A X = B（A 为 n×n，B 为 n×m，多右端）。
// 若 A 奇异则返回 ok=false，调用方必须据此拒绝给出有限解，而不是硬解。
func solveLinear(a [][]*big.Rat, b [][]*big.Rat) (x [][]*big.Rat, ok bool) {
	n := len(a)
	if n == 0 {
		return [][]*big.Rat{}, true
	}
	m := len(b[0])
	aug := make([][]*big.Rat, n)
	for i := range aug {
		aug[i] = make([]*big.Rat, n+m)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).Set(a[i][j])
		}
		for j := 0; j < m; j++ {
			aug[i][n+j] = new(big.Rat).Set(b[i][j])
		}
	}
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if aug[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil, false // 奇异：不存在唯一解
		}
		aug[col], aug[piv] = aug[piv], aug[col]
		inv := new(big.Rat).Inv(aug[col][col])
		for j := col; j < n+m; j++ {
			aug[col][j].Mul(aug[col][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := new(big.Rat).Set(aug[r][col]) // 拷贝，避免与 aug[r][col] 别名
			if f.Sign() == 0 {
				continue
			}
			for j := col; j < n+m; j++ {
				t := new(big.Rat).Mul(f, aug[col][j])
				aug[r][j].Sub(aug[r][j], t)
			}
		}
	}
	x = make([][]*big.Rat, n)
	for i := range x {
		x[i] = make([]*big.Rat, m)
		for j := 0; j < m; j++ {
			x[i][j] = aug[i][n+j]
		}
	}
	return x, true
}
