package ratbez

import "math/big"

// Poly is a univariate polynomial with exact rational coefficients.
// c[i] is the coefficient of x^i. The zero polynomial has len(c) == 0.
// All arithmetic is exact (math/big), no floating point is ever used.
type Poly struct{ c []*big.Rat }

func normalize(c []*big.Rat) Poly {
	n := len(c)
	for n > 0 && (c[n-1] == nil || c[n-1].Sign() == 0) {
		n--
	}
	return Poly{c: c[:n]}
}

// NewPoly builds a polynomial from coefficients (lowest degree first), copying them.
func NewPoly(c ...*big.Rat) Poly {
	cc := make([]*big.Rat, len(c))
	for i, v := range c {
		cc[i] = new(big.Rat).Set(v)
	}
	return normalize(cc)
}

// PolyInts builds a polynomial from integer coefficients (lowest degree first).
func PolyInts(vals ...int64) Poly {
	cc := make([]*big.Rat, len(vals))
	for i, v := range vals {
		cc[i] = big.NewRat(v, 1)
	}
	return normalize(cc)
}

func (p Poly) Deg() int       { return len(p.c) - 1 }
func (p Poly) IsZero() bool   { return len(p.c) == 0 }
func (p Poly) NumCoeffs() int { return len(p.c) }

func (p Poly) coeff(i int) *big.Rat {
	if i >= 0 && i < len(p.c) && p.c[i] != nil {
		return p.c[i]
	}
	return new(big.Rat)
}

// Coeff returns a copy of the coefficient of x^i (zero if out of range).
func (p Poly) Coeff(i int) *big.Rat { return new(big.Rat).Set(p.coeff(i)) }

func (p Poly) Lead() *big.Rat { return p.coeff(p.Deg()) }

func Add(a, b Poly) Poly {
	n := len(a.c)
	if len(b.c) > n {
		n = len(b.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(a.coeff(i), b.coeff(i))
	}
	return normalize(c)
}

func Neg(a Poly) Poly {
	c := make([]*big.Rat, len(a.c))
	for i, v := range a.c {
		c[i] = new(big.Rat).Neg(v)
	}
	return normalize(c)
}

func Sub(a, b Poly) Poly { return Add(a, Neg(b)) }

func Scale(a Poly, r *big.Rat) Poly {
	if r.Sign() == 0 {
		return Poly{}
	}
	c := make([]*big.Rat, len(a.c))
	for i, v := range a.c {
		c[i] = new(big.Rat).Mul(v, r)
	}
	return normalize(c)
}

func Mul(a, b Poly) Poly {
	if a.IsZero() || b.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, len(a.c)+len(b.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, ai := range a.c {
		for j, bj := range b.c {
			c[i+j].Add(c[i+j], new(big.Rat).Mul(ai, bj))
		}
	}
	return normalize(c)
}

func (p Poly) Deriv() Poly {
	if len(p.c) <= 1 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Rat).Mul(p.c[i], big.NewRat(int64(i), 1))
	}
	return normalize(c)
}

// QuoRem divides a by b over the rationals: a = q*b + r with deg(r) < deg(b).
func QuoRem(a, b Poly) (Poly, Poly) {
	if b.IsZero() {
		panic("polynomial division by zero")
	}
	r := make([]*big.Rat, len(a.c))
	for i := range a.c {
		r[i] = new(big.Rat).Set(a.c[i])
	}
	var q []*big.Rat
	if a.Deg() >= b.Deg() {
		q = make([]*big.Rat, a.Deg()-b.Deg()+1)
		for i := range q {
			q[i] = new(big.Rat)
		}
	}
	bd := b.Deg()
	rdeg := a.Deg()
	for rdeg >= bd {
		f := new(big.Rat).Quo(r[rdeg], b.Lead())
		k := rdeg - bd
		q[k] = new(big.Rat).Set(f)
		for j := 0; j <= bd; j++ {
			r[k+j] = new(big.Rat).Sub(r[k+j], new(big.Rat).Mul(f, b.coeff(j)))
		}
		rdeg--
		for rdeg >= 0 && r[rdeg].Sign() == 0 {
			rdeg--
		}
	}
	rem := Poly{}
	if rdeg >= 0 {
		rem = normalize(r[:rdeg+1])
	}
	return normalize(q), rem
}

// Monic returns p scaled so its leading coefficient is 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return Scale(p, new(big.Rat).Inv(p.Lead()))
}

// GCD is the monic greatest common divisor of a and b over the rationals.
func GCD(a, b Poly) Poly {
	for !b.IsZero() {
		_, r := QuoRem(a, b)
		a, b = b, r
	}
	return a.Monic()
}

// SquareFree returns the monic square-free part of p: p / gcd(p, p').
// Its roots are exactly the distinct roots of p.
func SquareFree(p Poly) Poly {
	if p.IsZero() {
		return p
	}
	g := GCD(p, p.Deriv())
	q, _ := QuoRem(p, g)
	return q.Monic()
}

// Eval evaluates p at the rational point x exactly (Horner).
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.coeff(i))
	}
	return acc
}

// ToPrimitive clears denominators and divides out the content, returning
// integer coefficients (lowest degree first). The sign is preserved.
func (p Poly) ToPrimitive() []*big.Int {
	lcm := big.NewInt(1)
	for _, ci := range p.c {
		d := ci.Denom()
		g := new(big.Int).GCD(nil, nil, lcm, d)
		lcm.Quo(lcm, g)
		lcm.Mul(lcm, d)
	}
	ints := make([]*big.Int, len(p.c))
	for i, ci := range p.c {
		ints[i] = new(big.Int).Mul(ci.Num(), new(big.Int).Quo(lcm, ci.Denom()))
	}
	g := new(big.Int)
	for _, v := range ints {
		g.GCD(nil, nil, g, v)
	}
	if g.Sign() > 0 {
		for _, v := range ints {
			v.Quo(v, g)
		}
	}
	return ints
}

// CauchyBound returns a strict rational bound B such that every root x of p
// satisfies |x| < B.
func CauchyBound(p Poly) *big.Rat {
	max := new(big.Rat)
	for i := 0; i < p.Deg(); i++ {
		a := new(big.Rat).Abs(p.coeff(i))
		if a.Cmp(max) > 0 {
			max = a
		}
	}
	lead := new(big.Rat).Abs(p.Lead())
	return new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(max, lead))
}

func polyToStrings(p Poly) []string {
	s := make([]string, len(p.c))
	for i, c := range p.c {
		s[i] = c.RatString()
	}
	return s
}

func intsToStrings(is []*big.Int) []string {
	s := make([]string, len(is))
	for i, v := range is {
		s[i] = v.String()
	}
	return s
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
