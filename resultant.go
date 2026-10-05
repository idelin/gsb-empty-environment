package ratbez

import "math/big"

// resultantFormal computes the Sylvester resultant of a (exact degree m) and
// b (treated with formal degree degB, padding with zeros if the actual degree
// is smaller) using exact rational Gaussian elimination.
func resultantFormal(a, b Poly, degB int) *big.Rat {
	m := a.Deg()
	n := degB
	size := m + n
	mat := make([][]*big.Rat, size)
	for i := range mat {
		mat[i] = make([]*big.Rat, size)
		for j := range mat[i] {
			mat[i][j] = new(big.Rat)
		}
	}
	for r := 0; r < n; r++ {
		for i := 0; i <= m; i++ {
			mat[r][r+i] = new(big.Rat).Set(a.coeff(i))
		}
	}
	for r := 0; r < m; r++ {
		for i := 0; i <= n; i++ {
			mat[n+r][r+i] = new(big.Rat).Set(b.coeff(i))
		}
	}
	return detRat(mat)
}

// detRat computes the determinant of a rational square matrix exactly by
// Gaussian elimination (modifies mat).
func detRat(mat [][]*big.Rat) *big.Rat {
	n := len(mat)
	det := big.NewRat(1, 1)
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if mat[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return new(big.Rat)
		}
		if piv != col {
			mat[piv], mat[col] = mat[col], mat[piv]
			det.Neg(det)
		}
		pv := new(big.Rat).Set(mat[col][col])
		det.Mul(det, pv)
		inv := new(big.Rat).Inv(pv)
		for r := col + 1; r < n; r++ {
			if mat[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Mul(mat[r][col], inv)
			for c := col; c < n; c++ {
				mat[r][c].Sub(mat[r][c], new(big.Rat).Mul(f, mat[col][c]))
			}
		}
	}
	return det
}

// valuePoly returns a polynomial S(y) whose roots include every value
// R(alpha) = N1(alpha)/D1(alpha) where alpha is a root of mf (the square-free
// derivative numerator) with D1(alpha) != 0. It is computed as
// S(y) = Res_t(mf, N1 - y*D1) by exact rational interpolation of m+1
// Sylvester resultants (m = deg mf).
func valuePoly(mf, N1, D1 Poly) Poly {
	m := mf.Deg()
	degB := N1.Deg()
	if D1.Deg() > degB {
		degB = D1.Deg()
	}
	xs := make([]*big.Rat, m+1)
	ys := make([]*big.Rat, m+1)
	for k := 0; k <= m; k++ {
		yk := big.NewRat(int64(k), 1)
		b := Sub(N1, Scale(D1, yk))
		xs[k] = yk
		ys[k] = resultantFormal(mf, b, degB)
	}
	return interpolate(xs, ys)
}

// interpolate returns the unique polynomial of degree < n passing through
// the n points (xs[i], ys[i]) (Lagrange interpolation, exact).
func interpolate(xs, ys []*big.Rat) Poly {
	n := len(xs)
	result := Poly{}
	for i := 0; i < n; i++ {
		li := PolyInts(1)
		denom := big.NewRat(1, 1)
		for j := 0; j < n; j++ {
			if j == i {
				continue
			}
			li = Mul(li, NewPoly(new(big.Rat).Neg(xs[j]), big.NewRat(1, 1)))
			denom.Mul(denom, new(big.Rat).Sub(xs[i], xs[j]))
		}
		li = Scale(li, new(big.Rat).Inv(denom))
		result = Add(result, Scale(li, ys[i]))
	}
	return result
}
