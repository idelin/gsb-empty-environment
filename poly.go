// Package rootfind finds all real roots of a rational polynomial inside a
// closed interval, with certified isolating intervals and multiplicities.
//
// Completeness is guaranteed by exact root counting (Sturm sequences) over
// rational arithmetic, combined with bisection. It never relies on sign
// changes of sampled values, so roots of even multiplicity (where the curve
// does not cross zero) are found as well.
package rootfind

import "math/big"

// Poly is a univariate polynomial with rational coefficients.
// coeffs[i] is the coefficient of x^i. The zero polynomial has no coeffs.
type Poly struct {
	coeffs []*big.Rat
}

// NewPoly builds a polynomial from coefficients in ascending degree order.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, a := range coeffs {
		if a == nil {
			c[i] = new(big.Rat)
		} else {
			c[i] = new(big.Rat).Set(a)
		}
	}
	return Poly{trim(c)}
}

// PolyFromInts builds a polynomial from integer coefficients.
func PolyFromInts(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, a := range coeffs {
		c[i] = big.NewRat(a, 1)
	}
	return NewPoly(c...)
}

// PolyFromStrings builds a polynomial from rationals like "3/2" or "-4".
func PolyFromStrings(coeffs ...string) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, s := range coeffs {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic("rootfind: bad rational " + s)
		}
		c[i] = r
	}
	return NewPoly(c...)
}

// Rat parses a rational from a string like "3/2".
func Rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("rootfind: bad rational " + s)
	}
	return r
}

func trim(c []*big.Rat) []*big.Rat {
	n := len(c)
	for n > 0 && (c[n-1] == nil || c[n-1].Sign() == 0) {
		n--
	}
	return c[:n]
}

// Deg returns the degree; the zero polynomial has degree -1.
func (p Poly) Deg() int { return len(p.coeffs) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.coeffs) == 0 }

// Coeff returns the coefficient of x^i (zero when out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.coeffs[i])
}

func (p Poly) lc() *big.Rat { return p.coeffs[len(p.coeffs)-1] }

// Add returns a+b.
func Add(a, b Poly) Poly {
	n := len(a.coeffs)
	if len(b.coeffs) > n {
		n = len(b.coeffs)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat)
		if i < len(a.coeffs) {
			c[i].Add(c[i], a.coeffs[i])
		}
		if i < len(b.coeffs) {
			c[i].Add(c[i], b.coeffs[i])
		}
	}
	return Poly{trim(c)}
}

// Neg returns -a.
func Neg(a Poly) Poly {
	c := make([]*big.Rat, len(a.coeffs))
	for i, v := range a.coeffs {
		c[i] = new(big.Rat).Neg(v)
	}
	return Poly{c}
}

// Sub returns a-b.
func Sub(a, b Poly) Poly { return Add(a, Neg(b)) }

// Mul returns a*b.
func Mul(a, b Poly) Poly {
	if a.IsZero() || b.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, a.Deg()+b.Deg()+1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, ai := range a.coeffs {
		if ai.Sign() == 0 {
			continue
		}
		for j, bj := range b.coeffs {
			if bj.Sign() == 0 {
				continue
			}
			c[i+j].Add(c[i+j], new(big.Rat).Mul(ai, bj))
		}
	}
	return Poly{trim(c)}
}

// Scale multiplies p by a rational constant.
func Scale(p Poly, s *big.Rat) Poly {
	c := make([]*big.Rat, len(p.coeffs))
	for i, v := range p.coeffs {
		c[i] = new(big.Rat).Mul(v, s)
	}
	return Poly{trim(c)}
}

// DivMod returns quotient and remainder of a/b. It panics if b is zero.
func DivMod(a, b Poly) (Poly, Poly) {
	if b.IsZero() {
		panic("rootfind: polynomial division by zero")
	}
	rem := make([]*big.Rat, len(a.coeffs))
	for i, v := range a.coeffs {
		rem[i] = new(big.Rat).Set(v)
	}
	if a.Deg() < b.Deg() {
		return Poly{}, Poly{trim(rem)}
	}
	quo := make([]*big.Rat, a.Deg()-b.Deg()+1)
	for i := range quo {
		quo[i] = new(big.Rat)
	}
	invLead := new(big.Rat).Inv(b.lc())
	bd := b.Deg()
	for k := a.Deg() - bd; k >= 0; k-- {
		c := new(big.Rat).Mul(rem[k+bd], invLead)
		quo[k].Set(c)
		if c.Sign() == 0 {
			continue
		}
		for j := 0; j <= bd; j++ {
			rem[k+j].Sub(rem[k+j], new(big.Rat).Mul(c, b.coeffs[j]))
		}
	}
	return Poly{trim(quo)}, Poly{trim(rem)}
}

// Deriv returns the formal derivative.
func (p Poly) Deriv() Poly {
	if p.Deg() <= 0 {
		return Poly{}
	}
	c := make([]*big.Rat, p.Deg())
	for i := 1; i <= p.Deg(); i++ {
		c[i-1] = new(big.Rat).Mul(p.coeffs[i], big.NewRat(int64(i), 1))
	}
	return Poly{trim(c)}
}

// Monic returns p scaled so its leading coefficient is 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	inv := new(big.Rat).Inv(p.lc())
	return Scale(p, inv)
}

// GCD returns the monic greatest common divisor of a and b.
func GCD(a, b Poly) Poly {
	for !b.IsZero() {
		_, r := DivMod(a, b)
		a, b = b, r
	}
	return a.Monic()
}

// Eval evaluates p at the rational point x (Horner's rule, exact).
func (p Poly) Eval(x *big.Rat) *big.Rat {
	r := new(big.Rat)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		r.Mul(r, x)
		r.Add(r, p.coeffs[i])
	}
	return r
}

// Pow returns p^n for n >= 0.
func Pow(p Poly, n int) Poly {
	r := PolyFromInts(1)
	for i := 0; i < n; i++ {
		r = Mul(r, p)
	}
	return r
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	s := ""
	for i := p.Deg(); i >= 0; i-- {
		c := p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		term := c.RatString()
		if i > 0 {
			term += "*x"
			if i > 1 {
				term += "^" + itoa(i)
			}
		}
		if s != "" {
			s += " + "
		}
		s += term
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
