package algebraic

import (
	"fmt"
	"math/big"
	"strings"
)

type poly []*big.Rat

func ratInt(n int64) *big.Rat {
	return big.NewRat(n, 1)
}

func cloneRat(x *big.Rat) *big.Rat {
	return new(big.Rat).Set(x)
}

func clonePoly(p poly) poly {
	out := make(poly, len(p))
	for i, c := range p {
		out[i] = cloneRat(c)
	}
	return out
}

func normPoly(p poly) poly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func polyDegree(p poly) int {
	return len(p) - 1
}

func polyIsZero(p poly) bool {
	return len(normPoly(p)) == 0
}

func polyLeading(p poly) *big.Rat {
	if len(p) == 0 {
		return ratInt(0)
	}
	return p[len(p)-1]
}

func polyAdd(a, b poly) poly {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make(poly, n)
	for i := 0; i < n; i++ {
		out[i] = ratInt(0)
		if i < len(a) {
			out[i].Add(out[i], a[i])
		}
		if i < len(b) {
			out[i].Add(out[i], b[i])
		}
	}
	return normPoly(out)
}

func polyNeg(a poly) poly {
	out := make(poly, len(a))
	for i, c := range a {
		out[i] = new(big.Rat).Neg(c)
	}
	return out
}

func polySub(a, b poly) poly {
	return polyAdd(a, polyNeg(b))
}

func polyScale(a poly, s *big.Rat) poly {
	if s.Sign() == 0 {
		return poly{}
	}
	out := make(poly, len(a))
	for i, c := range a {
		out[i] = new(big.Rat).Mul(c, s)
	}
	return normPoly(out)
}

func polyMul(a, b poly) poly {
	if len(a) == 0 || len(b) == 0 {
		return poly{}
	}
	out := make(poly, len(a)+len(b)-1)
	for i := range out {
		out[i] = ratInt(0)
	}
	for i, ac := range a {
		for j, bc := range b {
			term := new(big.Rat).Mul(ac, bc)
			out[i+j].Add(out[i+j], term)
		}
	}
	return normPoly(out)
}

func polyShift(a poly, n int) poly {
	if len(a) == 0 {
		return poly{}
	}
	out := make(poly, len(a)+n)
	for i := 0; i < n; i++ {
		out[i] = ratInt(0)
	}
	for i, c := range a {
		out[i+n] = cloneRat(c)
	}
	return out
}

func polyDivMod(a, b poly) (poly, poly) {
	a = normPoly(clonePoly(a))
	b = normPoly(b)
	if len(b) == 0 {
		panic("polynomial division by zero")
	}
	if len(a) < len(b) {
		return poly{}, a
	}
	q := make(poly, len(a)-len(b)+1)
	for i := range q {
		q[i] = ratInt(0)
	}
	r := a
	for len(r) >= len(b) && len(r) > 0 {
		shift := len(r) - len(b)
		coeff := new(big.Rat).Quo(polyLeading(r), polyLeading(b))
		q[shift] = coeff
		sub := polyShift(polyScale(b, coeff), shift)
		r = polySub(r, sub)
	}
	return normPoly(q), normPoly(r)
}

func polyDivExact(a, b poly) poly {
	q, r := polyDivMod(a, b)
	if len(r) != 0 {
		panic("polynomial division is not exact")
	}
	return q
}

func polyGCD(a, b poly) poly {
	a = normPoly(clonePoly(a))
	b = normPoly(clonePoly(b))
	for len(b) != 0 {
		_, r := polyDivMod(a, b)
		a, b = b, r
	}
	return polyMonic(a)
}

func polyMonic(p poly) poly {
	p = normPoly(clonePoly(p))
	if len(p) == 0 {
		return p
	}
	lc := polyLeading(p)
	if lc.Cmp(ratInt(1)) == 0 {
		return p
	}
	inv := new(big.Rat).Inv(lc)
	return polyScale(p, inv)
}

func polyDerivative(p poly) poly {
	if len(p) <= 1 {
		return poly{}
	}
	out := make(poly, len(p)-1)
	for i := 1; i < len(p); i++ {
		out[i-1] = new(big.Rat).Mul(p[i], big.NewRat(int64(i), 1))
	}
	return normPoly(out)
}

func polyEval(p poly, x *big.Rat) *big.Rat {
	acc := ratInt(0)
	for i := len(p) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p[i])
	}
	return acc
}

func polyEqual(a, b poly) bool {
	a = normPoly(a)
	b = normPoly(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(b[i]) != 0 {
			return false
		}
	}
	return true
}

func polyComposeSquare(p poly) poly {
	if len(p) == 0 {
		return poly{}
	}
	out := make(poly, 2*len(p)-1)
	for i := range out {
		out[i] = ratInt(0)
	}
	for i, c := range p {
		out[2*i] = cloneRat(c)
	}
	return normPoly(out)
}

func polyNegX(p poly) poly {
	out := clonePoly(p)
	for i := 1; i < len(out); i += 2 {
		out[i].Neg(out[i])
	}
	return normPoly(out)
}

func polyReverse(p poly) poly {
	out := make(poly, len(p))
	for i := range p {
		out[len(p)-1-i] = cloneRat(p[i])
	}
	return normPoly(out)
}

func polyString(p poly) string {
	p = normPoly(p)
	if len(p) == 0 {
		return "0"
	}
	parts := make([]string, 0, len(p))
	for i := len(p) - 1; i >= 0; i-- {
		c := p[i]
		if c.Sign() == 0 {
			continue
		}
		abs := new(big.Rat).Abs(c)
		coeff := abs.RatString()
		term := ""
		if i > 0 {
			term = "x"
			if i > 1 {
				term = fmt.Sprintf("x^%d", i)
			}
			if abs.Cmp(ratInt(1)) != 0 {
				term = coeff + "*" + term
			}
		} else {
			term = coeff
		}
		if len(parts) == 0 {
			if c.Sign() < 0 {
				term = "-" + term
			}
		} else if c.Sign() < 0 {
			term = "- " + term
		} else {
			term = "+ " + term
		}
		parts = append(parts, term)
	}
	return strings.Join(parts, " ")
}

func determinant(matrix [][]*big.Rat) *big.Rat {
	n := len(matrix)
	a := make([][]*big.Rat, n)
	for i := range matrix {
		a[i] = make([]*big.Rat, n)
		for j := range matrix[i] {
			a[i][j] = cloneRat(matrix[i][j])
		}
	}
	det := ratInt(1)
	for col := 0; col < n; col++ {
		pivot := col
		for pivot < n && a[pivot][col].Sign() == 0 {
			pivot++
		}
		if pivot == n {
			return ratInt(0)
		}
		if pivot != col {
			a[pivot], a[col] = a[col], a[pivot]
			det.Neg(det)
		}
		pv := cloneRat(a[col][col])
		det.Mul(det, pv)
		for row := col + 1; row < n; row++ {
			if a[row][col].Sign() == 0 {
				continue
			}
			factor := new(big.Rat).Quo(a[row][col], pv)
			for j := col; j < n; j++ {
				term := new(big.Rat).Mul(factor, a[col][j])
				a[row][j].Sub(a[row][j], term)
			}
		}
	}
	return det
}

func resultant(a, b poly) *big.Rat {
	a = normPoly(a)
	b = normPoly(b)
	if len(a) == 0 || len(b) == 0 {
		return ratInt(0)
	}
	if len(a) == 1 && len(b) == 1 {
		return ratInt(1)
	}
	if len(a) == 1 {
		out := ratInt(1)
		for i := 0; i < len(b)-1; i++ {
			out.Mul(out, a[0])
		}
		return out
	}
	if len(b) == 1 {
		out := ratInt(1)
		for i := 0; i < len(a)-1; i++ {
			out.Mul(out, b[0])
		}
		return out
	}
	m := len(a) - 1
	n := len(b) - 1
	size := m + n
	matrix := make([][]*big.Rat, size)
	for i := range matrix {
		matrix[i] = make([]*big.Rat, size)
		for j := range matrix[i] {
			matrix[i][j] = ratInt(0)
		}
	}
	for row := 0; row < n; row++ {
		for j := 0; j <= m; j++ {
			matrix[row][row+j] = cloneRat(a[j])
		}
	}
	for row := 0; row < m; row++ {
		for j := 0; j <= n; j++ {
			matrix[n+row][row+j] = cloneRat(b[j])
		}
	}
	return determinant(matrix)
}

func interpolate(xs []int64, ys []*big.Rat) poly {
	out := poly{}
	for i := range xs {
		basis := poly{ratInt(1)}
		den := ratInt(1)
		for j := range xs {
			if i == j {
				continue
			}
			basis = polyMul(basis, poly{ratInt(-xs[j]), ratInt(1)})
			den.Mul(den, big.NewRat(xs[i]-xs[j], 1))
		}
		scale := new(big.Rat).Quo(ys[i], den)
		out = polyAdd(out, polyScale(basis, scale))
	}
	return normPoly(out)
}

func ratPowInt(x *big.Rat, n int) *big.Rat {
	out := ratInt(1)
	base := cloneRat(x)
	for n > 0 {
		if n%2 == 1 {
			out.Mul(out, base)
		}
		n /= 2
		if n > 0 {
			base.Mul(base, base)
		}
	}
	return out
}

func shiftForAdd(b poly, t int64) poly {
	out := poly{}
	tr := big.NewRat(t, 1)
	for j, coeff := range b {
		for k := 0; k <= j; k++ {
			bin := binomialRat(j, k)
			pow := ratPowInt(tr, j-k)
			term := new(big.Rat).Mul(coeff, bin)
			term.Mul(term, pow)
			if k%2 == 1 {
				term.Neg(term)
			}
			for len(out) <= k {
				out = append(out, ratInt(0))
			}
			out[k].Add(out[k], term)
		}
	}
	return normPoly(out)
}

func shiftForMul(b poly, t int64) poly {
	n := len(b) - 1
	out := make(poly, n+1)
	for i := range out {
		out[i] = ratInt(0)
	}
	tr := big.NewRat(t, 1)
	for j, coeff := range b {
		pow := ratPowInt(tr, j)
		term := new(big.Rat).Mul(coeff, pow)
		out[n-j].Add(out[n-j], term)
	}
	return normPoly(out)
}

func binomialRat(n, k int) *big.Rat {
	if k < 0 || k > n {
		return ratInt(0)
	}
	if k > n-k {
		k = n - k
	}
	out := big.NewInt(1)
	for i := 0; i < k; i++ {
		out.Mul(out, big.NewInt(int64(n-i)))
		out.Div(out, big.NewInt(int64(i+1)))
	}
	return new(big.Rat).SetInt(out)
}

func resultantAdd(a, b poly) poly {
	degree := (len(a) - 1) * (len(b) - 1)
	xs := make([]int64, degree+1)
	ys := make([]*big.Rat, degree+1)
	for i := 0; i <= degree; i++ {
		xs[i] = int64(i)
		ys[i] = resultant(a, shiftForAdd(b, xs[i]))
	}
	return polyMonic(interpolate(xs, ys))
}

func resultantMul(a, b poly) poly {
	degree := (len(a) - 1) * (len(b) - 1)
	xs := make([]int64, degree+1)
	ys := make([]*big.Rat, degree+1)
	for i := 0; i <= degree; i++ {
		xs[i] = int64(i)
		ys[i] = resultant(a, shiftForMul(b, xs[i]))
	}
	return polyMonic(interpolate(xs, ys))
}
