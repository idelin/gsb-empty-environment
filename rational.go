package taskpkg

import "math/big"

// ratZero 是共享的零有理数，调用方不得修改。
var ratZero = big.NewRat(0, 1)

// cloneRat 返回 x 的独立副本（nil 透传为 nil）。
func cloneRat(x *big.Rat) *big.Rat {
	if x == nil {
		return nil
	}
	return new(big.Rat).Set(x)
}

// ratEq 报告两个有理数是否精确相等（nil 仅与 nil 相等）。
func ratEq(a, b *big.Rat) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

// ratAbs 返回 |x|（新分配）。
func ratAbs(x *big.Rat) *big.Rat {
	if x.Sign() < 0 {
		return new(big.Rat).Neg(x)
	}
	return new(big.Rat).Set(x)
}

// ratSquare 返回 x*x（新分配）。
func ratSquare(x *big.Rat) *big.Rat {
	return new(big.Rat).Mul(x, x)
}

// ratMax 返回 a、b 中的较大者（不修改入参）。
func ratMax(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// dot3 返回 a·b（新分配）。
func dot3(a, b [3]*big.Rat) *big.Rat {
	s := new(big.Rat)
	for i := 0; i < 3; i++ {
		s.Add(s, new(big.Rat).Mul(a[i], b[i]))
	}
	return s
}

// scaledRow 返回标量 s 乘向量 v（新分配）。
func scaledRow(s *big.Rat, v [3]*big.Rat) [3]*big.Rat {
	var out [3]*big.Rat
	for i := 0; i < 3; i++ {
		out[i] = new(big.Rat).Mul(s, v[i])
	}
	return out
}

// det3 返回 3x3 有理矩阵的行列式（新分配）。
func det3(m [3][3]*big.Rat) *big.Rat {
	d := new(big.Rat)
	for c := 0; c < 3; c++ {
		minor := new(big.Rat).Mul(m[1][(c+1)%3], m[2][(c+2)%3])
		minor.Sub(minor, new(big.Rat).Mul(m[1][(c+2)%3], m[2][(c+1)%3]))
		term := new(big.Rat).Mul(m[0][c], minor)
		if c%2 == 0 {
			d.Add(d, term)
		} else {
			d.Sub(d, term)
		}
	}
	return d
}

// replaceCol 返回把矩阵 m 的第 col 列替换为 col 后的新矩阵。
func replaceCol(m [3][3]*big.Rat, col int, vals [3]*big.Rat) [3][3]*big.Rat {
	var out [3][3]*big.Rat
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			if c == col {
				out[r][c] = new(big.Rat).Set(vals[r])
			} else {
				out[r][c] = new(big.Rat).Set(m[r][c])
			}
		}
	}
	return out
}

// solveCramer 用克拉默法则求解满秩 3x3 有理线性系统 m·g = rhs。
// 调用方需保证 det(m) 非零；返回精确有理解。
func solveCramer(m [3][3]*big.Rat, det *big.Rat, rhs [3]*big.Rat) [3]*big.Rat {
	var g [3]*big.Rat
	for c := 0; c < 3; c++ {
		num := det3(replaceCol(m, c, rhs))
		g[c] = num.Quo(num, det)
	}
	return g
}

// ratToFloat 把有理数安全转换为 float64。
func ratToFloat(x *big.Rat) float64 {
	f, _ := x.Float64()
	return f
}
