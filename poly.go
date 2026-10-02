package algebraic

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with rational coefficients.
// c[i] holds the coefficient of x^i. The invariant is that the
// leading coefficient is never zero; the zero polynomial has len(c)==0.
type Poly struct {
	c []*big.Rat
}

// NewPoly builds a polynomial from coefficients, lowest degree first.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, cf := range coeffs {
		c[i] = new(big.Rat).Set(cf)
	}
	return polyFromCoeffs(c)
}

// RatPoly builds a polynomial from integer coefficients, lowest degree first.
func RatPoly(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, k := range coeffs {
		c[i] = big.NewRat(k, 1)
	}
	return polyFromCoeffs(c)
}

func polyFromCoeffs(c []*big.Rat) Poly {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return Poly{c: c[:n]}
}

func (p Poly) IsZero() bool { return len(p.c) == 0 }

func (p Poly) IsOne() bool { return len(p.c) == 1 && p.c[0].Cmp(big.NewRat(1, 1)) == 0 }

// Deg returns the degree; the zero polynomial has degree -1.
func (p Poly) Deg() int { return len(p.c) - 1 }

func (p Poly) coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return p.c[i]
}

func (p Poly) Lead() *big.Rat {
	if p.IsZero() {
		return new(big.Rat)
	}
	return p.c[len(p.c)-1]
}

func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	inv := new(big.Rat).Inv(p.Lead())
	c := make([]*big.Rat, len(p.c))
	for i, cf := range p.c {
		c[i] = new(big.Rat).Mul(cf, inv)
	}
	return Poly{c: c}
}

func (p Poly) Neg() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, cf := range p.c {
		c[i] = new(big.Rat).Neg(cf)
	}
	return Poly{c: c}
}

func (p Poly) Add(q Poly) Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(p.coeff(i), q.coeff(i))
	}
	return polyFromCoeffs(c)
}

func (p Poly) Sub(q Poly) Poly { return p.Add(q.Neg()) }

func (p Poly) Mul(q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)+len(q.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, a := range p.c {
		for j, b := range q.c {
			c[i+j].Add(c[i+j], new(big.Rat).Mul(a, b))
		}
	}
	return polyFromCoeffs(c)
}

func (p Poly) Scale(s *big.Rat) Poly {
	c := make([]*big.Rat, len(p.c))
	for i, cf := range p.c {
		c[i] = new(big.Rat).Mul(cf, s)
	}
	return polyFromCoeffs(c)
}

// QuoRem returns quotient and remainder of p divided by q.
func (p Poly) QuoRem(q Poly) (quo, rem Poly) {
	if q.IsZero() {
		panic("algebraic: polynomial division by zero")
	}
	rem = p
	var qc []*big.Rat
	for !rem.IsZero() && rem.Deg() >= q.Deg() {
		s := rem.Deg() - q.Deg()
		coeff := new(big.Rat).Quo(rem.Lead(), q.Lead())
		for len(qc) <= s {
			qc = append(qc, new(big.Rat))
		}
		qc[s] = new(big.Rat).Set(coeff)
		t := make([]*big.Rat, s+len(q.c))
		for i := range t {
			t[i] = new(big.Rat)
		}
		for j, b := range q.c {
			t[s+j] = new(big.Rat).Mul(coeff, b)
		}
		rem = rem.Sub(polyFromCoeffs(t))
	}
	return polyFromCoeffs(qc), rem
}

// PolyGCD returns the monic greatest common divisor of a and b.
func PolyGCD(a, b Poly) Poly {
	for !b.IsZero() {
		_, r := a.QuoRem(b)
		a, b = b, r
	}
	return a.Monic()
}

func (p Poly) Deriv() Poly {
	if len(p.c) <= 1 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Rat).Mul(p.c[i], big.NewRat(int64(i), 1))
	}
	return polyFromCoeffs(c)
}

func (p Poly) Eval(x *big.Rat) *big.Rat {
	r := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		r.Mul(r, x)
		r.Add(r, p.c[i])
	}
	return r
}

// NegX returns p(-x).
func (p Poly) NegX() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, cf := range p.c {
		c[i] = new(big.Rat).Set(cf)
		if i%2 == 1 {
			c[i].Neg(c[i])
		}
	}
	return Poly{c: c}
}

// ComposeX2 returns p(x^2).
func (p Poly) ComposeX2() Poly {
	if p.IsZero() {
		return p
	}
	c := make([]*big.Rat, 2*len(p.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, cf := range p.c {
		c[2*i] = new(big.Rat).Set(cf)
	}
	return polyFromCoeffs(c)
}

// composeNegShift returns p(c - y) as a polynomial in y.
func (p Poly) composeNegShift(c *big.Rat) Poly {
	base := NewPoly(new(big.Rat).Set(c), big.NewRat(-1, 1))
	result := Poly{}
	power := NewPoly(big.NewRat(1, 1))
	for j := 0; j <= p.Deg(); j++ {
		result = result.Add(power.Scale(p.coeff(j)))
		power = power.Mul(base)
	}
	return result
}

func (p Poly) Equal(q Poly) bool {
	if len(p.c) != len(q.c) {
		return false
	}
	for i := range p.c {
		if p.c[i].Cmp(q.c[i]) != 0 {
			return false
		}
	}
	return true
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var sb strings.Builder
	first := true
	one := big.NewRat(1, 1)
	for i := p.Deg(); i >= 0; i-- {
		c := p.coeff(i)
		if c.Sign() == 0 {
			continue
		}
		neg := c.Sign() < 0
		abs := new(big.Rat).Abs(c)
		isOne := abs.Cmp(one) == 0
		if first {
			if neg {
				sb.WriteString("-")
			}
			first = false
		} else if neg {
			sb.WriteString(" - ")
		} else {
			sb.WriteString(" + ")
		}
		if !isOne || i == 0 {
			sb.WriteString(abs.RatString())
			if i > 0 {
				sb.WriteString("*")
			}
		}
		if i > 0 {
			sb.WriteString("x")
			if i > 1 {
				fmt.Fprintf(&sb, "^%d", i)
			}
		}
	}
	return sb.String()
}

// Resultant computes the resultant of f and g via the Sylvester determinant.
func Resultant(f, g Poly) *big.Rat {
	n, m := f.Deg(), g.Deg()
	if f.IsZero() || g.IsZero() {
		return new(big.Rat)
	}
	if n == 0 && m == 0 {
		return big.NewRat(1, 1)
	}
	if n == 0 {
		return ratPow(f.c[0], m)
	}
	if m == 0 {
		return ratPow(g.c[0], n)
	}
	size := n + m
	M := make([][]*big.Rat, size)
	for i := range M {
		M[i] = make([]*big.Rat, size)
		for j := range M[i] {
			M[i][j] = new(big.Rat)
		}
	}
	for i := 0; i < m; i++ {
		for k := 0; k <= n; k++ {
			M[i][i+k].Set(f.coeff(n - k))
		}
	}
	for j := 0; j < n; j++ {
		for k := 0; k <= m; k++ {
			M[m+j][j+k].Set(g.coeff(m - k))
		}
	}
	return detRat(M)
}

func ratPow(x *big.Rat, n int) *big.Rat {
	r := big.NewRat(1, 1)
	for i := 0; i < n; i++ {
		r.Mul(r, x)
	}
	return r
}

func detRat(M [][]*big.Rat) *big.Rat {
	n := len(M)
	A := make([][]*big.Rat, n)
	for i := range M {
		A[i] = make([]*big.Rat, n)
		for j := range M[i] {
			A[i][j] = new(big.Rat).Set(M[i][j])
		}
	}
	det := big.NewRat(1, 1)
	sign := 1
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if A[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return new(big.Rat)
		}
		if piv != col {
			A[col], A[piv] = A[piv], A[col]
			sign = -sign
		}
		pv := A[col][col]
		det.Mul(det, pv)
		for r := col + 1; r < n; r++ {
			if A[r][col].Sign() == 0 {
				continue
			}
			factor := new(big.Rat).Quo(A[r][col], pv)
			for c := col; c < n; c++ {
				A[r][c].Sub(A[r][c], new(big.Rat).Mul(factor, A[col][c]))
			}
		}
	}
	if sign < 0 {
		det.Neg(det)
	}
	return det
}

// interpolate returns the unique polynomial of degree < n passing through
// the n points (xs[i], ys[i]).
func interpolate(xs, ys []*big.Rat) Poly {
	n := len(xs)
	result := Poly{}
	for i := 0; i < n; i++ {
		num := NewPoly(big.NewRat(1, 1))
		den := big.NewRat(1, 1)
		for j := 0; j < n; j++ {
			if j == i {
				continue
			}
			num = num.Mul(NewPoly(new(big.Rat).Neg(xs[j]), big.NewRat(1, 1)))
			den.Mul(den, new(big.Rat).Sub(xs[i], xs[j]))
		}
		num = num.Scale(new(big.Rat).Quo(ys[i], den))
		result = result.Add(num)
	}
	return result
}

// polySumPoly returns a polynomial whose roots are exactly the sums
// alpha+beta with alpha a root of f and beta a root of g:
// h(x) = res_y(f(y), g(x-y)), computed by evaluation and interpolation.
func polySumPoly(f, g Poly) Poly {
	N := f.Deg() * g.Deg()
	xs := make([]*big.Rat, 0, N+1)
	ys := make([]*big.Rat, 0, N+1)
	for k := 0; k <= N; k++ {
		xk := big.NewRat(int64(k), 1)
		gy := g.composeNegShift(xk)
		xs = append(xs, xk)
		ys = append(ys, Resultant(f, gy))
	}
	return interpolate(xs, ys)
}

// polyProdPoly returns a polynomial whose roots are exactly the products
// alpha*beta with alpha a root of f and beta a root of g:
// h(x) = res_y(f(y), y^m g(x/y)) where m = deg(g).
func polyProdPoly(f, g Poly) Poly {
	m := g.Deg()
	N := f.Deg() * m
	xs := make([]*big.Rat, 0, N+1)
	ys := make([]*big.Rat, 0, N+1)
	for k := 0; k <= N; k++ {
		xk := big.NewRat(int64(k), 1)
		// Q(y) = y^m g(xk/y) = sum_j g_j xk^j y^(m-j)
		qc := make([]*big.Rat, m+1)
		for t := range qc {
			qc[t] = new(big.Rat)
		}
		for j := 0; j <= m; j++ {
			qc[m-j] = new(big.Rat).Mul(g.coeff(j), ratPow(xk, j))
		}
		xs = append(xs, xk)
		ys = append(ys, Resultant(f, polyFromCoeffs(qc)))
	}
	return interpolate(xs, ys)
}
