package main

import "math/big"

// resultantY computes R(y) = Res_t(q, n - y*d) as a polynomial in y.
// Its roots include all values n(alpha)/d(alpha) for roots alpha of q.
// Computed by evaluation at deg(q)+1 points plus Lagrange interpolation,
// so no polynomial-matrix determinant is needed.
func resultantY(q, n, d Poly) Poly {
	m := q.Deg()
	nd := max(n.Deg(), d.Deg())
	xs := make([]*big.Rat, m+1)
	ys := make([]*big.Rat, m+1)
	for j := 0; j <= m; j++ {
		yj := ratI(int64(j))
		g := subP(n, scaleP(d, yj))
		ys[j] = resultantConst(q, g, nd)
		xs[j] = yj
	}
	return interpolate(xs, ys)
}

// resultantConst computes Res_t(a, b) where b is taken with formal degree
// degB (leading zeros allowed), via the Sylvester determinant (Bareiss).
func resultantConst(a, b Poly, degB int) *big.Rat {
	na := a.Deg()
	size := na + degB
	if size == 0 {
		return ratI(1)
	}
	M := make([][]*big.Rat, size)
	for i := range M {
		M[i] = make([]*big.Rat, size)
		for j := range M[i] {
			M[i][j] = ratI(0)
		}
	}
	// first degB rows: coefficients of a, shifted
	for i := 0; i < degB; i++ {
		for j := 0; j <= na; j++ {
			M[i][i+j] = new(big.Rat).Set(a.Get(j))
		}
	}
	// next na rows: coefficients of b (padded to degree degB), shifted
	for i := 0; i < na; i++ {
		for j := 0; j <= degB; j++ {
			M[degB+i][i+j] = new(big.Rat).Set(b.Get(j))
		}
	}
	return bareissRat(M)
}

// bareissRat computes the determinant of a rational matrix by the
// fraction-free Bareiss algorithm with row pivoting.
func bareissRat(M [][]*big.Rat) *big.Rat {
	n := len(M)
	sign := 1
	prev := ratI(1)
	for k := 0; k < n-1; k++ {
		if M[k][k].Sign() == 0 {
			piv := -1
			for i := k + 1; i < n; i++ {
				if M[i][k].Sign() != 0 {
					piv = i
					break
				}
			}
			if piv < 0 {
				return ratI(0)
			}
			M[k], M[piv] = M[piv], M[k]
			sign = -sign
		}
		for i := k + 1; i < n; i++ {
			for j := k + 1; j < n; j++ {
				t := new(big.Rat).Sub(
					new(big.Rat).Mul(M[i][j], M[k][k]),
					new(big.Rat).Mul(M[i][k], M[k][j]))
				t.Quo(t, prev)
				M[i][j] = t
			}
		}
		prev = M[k][k]
	}
	r := new(big.Rat).Set(M[n-1][n-1])
	if sign < 0 {
		r.Neg(r)
	}
	return r
}

// interpolate returns the unique polynomial of degree <= len(xs)-1 through
// the given points (Lagrange form).
func interpolate(xs, ys []*big.Rat) Poly {
	r := Poly{}
	for i := range xs {
		num := Poly{ratI(1)}
		den := ratI(1)
		for j := range xs {
			if i == j {
				continue
			}
			num = mulP(num, Poly{new(big.Rat).Neg(xs[j]), ratI(1)})
			den.Mul(den, new(big.Rat).Sub(xs[i], xs[j]))
		}
		r = addP(r, scaleP(num, new(big.Rat).Quo(ys[i], den)))
	}
	return r
}
