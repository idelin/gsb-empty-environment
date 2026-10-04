// Package polygcd implements GCD and extended GCD for univariate
// polynomials over the rationals.
//
// Coefficients are arbitrary-precision rationals (math/big.Rat), which are
// always kept in lowest terms. The GCD is computed with a subresultant
// pseudo-remainder sequence over Z (after clearing denominators) to control
// coefficient growth, while Bezout cofactors are tracked alongside with the
// same subresultant normalization. Bit-length statistics of every
// intermediate coefficient are recorded in Stats.
//
// Conventions:
//   - The zero polynomial is the empty (nil) coefficient slice, degree -1.
//   - GCD(0, 0) is defined as 0, with Bezout coefficients S = T = 0.
//   - For nonzero inputs the returned GCD is always monic.
//   - The certificate satisfies S*A + T*B = G, and G divides both A and B.
package polygcd

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with rational coefficients.
// coeffs[i] is the coefficient of x^i. The representation is normalized:
// the highest coefficient is nonzero, and the zero polynomial is nil.
type Poly struct {
	coeffs []*big.Rat
}

// Zero returns the zero polynomial.
func Zero() Poly { return Poly{} }

// One returns the constant polynomial 1.
func One() Poly { return Const(big.NewRat(1, 1)) }

// Const returns a constant polynomial.
func Const(c *big.Rat) Poly {
	if c.Sign() == 0 {
		return Zero()
	}
	return Poly{coeffs: []*big.Rat{new(big.Rat).Set(c)}}
}

// NewPoly builds a polynomial from coefficients (lowest degree first).
func NewPoly(coeffs ...*big.Rat) Poly {
	p := Poly{coeffs: make([]*big.Rat, len(coeffs))}
	for i, c := range coeffs {
		if c == nil {
			p.coeffs[i] = new(big.Rat)
		} else {
			p.coeffs[i] = new(big.Rat).Set(c)
		}
	}
	return p.normalize()
}

// PolyFromInts builds a polynomial from integer coefficients.
func PolyFromInts(coeffs ...int64) Poly {
	cs := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		cs[i] = big.NewRat(c, 1)
	}
	return NewPoly(cs...)
}

// PolyFromRats builds a polynomial from "num/den" string coefficients.
func PolyFromRats(coeffs ...string) Poly {
	cs := make([]*big.Rat, len(coeffs))
	for i, s := range coeffs {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic("polygcd: bad rational literal " + s)
		}
		cs[i] = r
	}
	return NewPoly(cs...)
}

func (p Poly) normalize() Poly {
	n := len(p.coeffs)
	for n > 0 && p.coeffs[n-1].Sign() == 0 {
		n--
	}
	p.coeffs = p.coeffs[:n]
	return p
}

// Deg returns the degree; the zero polynomial has degree -1.
func (p Poly) Deg() int { return len(p.coeffs) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.coeffs) == 0 }

// IsConst reports whether p is a (possibly zero) constant.
func (p Poly) IsConst() bool { return len(p.coeffs) <= 1 }

// LC returns the leading coefficient (nil for the zero polynomial).
func (p Poly) LC() *big.Rat {
	if p.IsZero() {
		return nil
	}
	return new(big.Rat).Set(p.coeffs[len(p.coeffs)-1])
}

// Coeff returns the coefficient of x^i (zero if out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.coeffs[i])
}

// Equal reports polynomial equality.
func (p Poly) Equal(q Poly) bool {
	if len(p.coeffs) != len(q.coeffs) {
		return false
	}
	for i := range p.coeffs {
		if p.coeffs[i].Cmp(q.coeffs[i]) != 0 {
			return false
		}
	}
	return true
}

// Monic returns p scaled so its leading coefficient is 1.
// The zero polynomial is returned unchanged.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.coeffs[len(p.coeffs)-1]))
}

// Scale multiplies every coefficient by r.
func (p Poly) Scale(r *big.Rat) Poly {
	if r.Sign() == 0 || p.IsZero() {
		return Zero()
	}
	out := make([]*big.Rat, len(p.coeffs))
	for i, c := range p.coeffs {
		out[i] = new(big.Rat).Mul(c, r)
	}
	return Poly{coeffs: out}.normalize()
}

// Add returns p + q.
func Add(p, q Poly) Poly {
	n := len(p.coeffs)
	if len(q.coeffs) > n {
		n = len(q.coeffs)
	}
	out := make([]*big.Rat, n)
	for i := range out {
		out[i] = new(big.Rat)
		if i < len(p.coeffs) {
			out[i].Add(out[i], p.coeffs[i])
		}
		if i < len(q.coeffs) {
			out[i].Add(out[i], q.coeffs[i])
		}
	}
	return Poly{coeffs: out}.normalize()
}

// Neg returns -p.
func Neg(p Poly) Poly {
	out := make([]*big.Rat, len(p.coeffs))
	for i, c := range p.coeffs {
		out[i] = new(big.Rat).Neg(c)
	}
	return Poly{coeffs: out}
}

// Sub returns p - q.
func Sub(p, q Poly) Poly { return Add(p, Neg(q)) }

// Mul returns p * q (schoolbook multiplication).
func Mul(p, q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Zero()
	}
	out := make([]*big.Rat, len(p.coeffs)+len(q.coeffs)-1)
	for i := range out {
		out[i] = new(big.Rat)
	}
	for i, a := range p.coeffs {
		if a.Sign() == 0 {
			continue
		}
		for j, b := range q.coeffs {
			if b.Sign() == 0 {
				continue
			}
			out[i+j].Add(out[i+j], new(big.Rat).Mul(a, b))
		}
	}
	return Poly{coeffs: out}.normalize()
}

// DivMod returns quotient and remainder of p divided by d over Q[x].
// It panics if d is the zero polynomial.
func DivMod(p, d Poly) (quo, rem Poly) {
	if d.IsZero() {
		panic("polygcd: division by zero polynomial")
	}
	rem = p
	quo = Zero()
	dlc := d.coeffs[len(d.coeffs)-1]
	dd := d.Deg()
	capHint := p.Deg() - dd + 1
	if capHint < 0 {
		capHint = 0
	}
	qc := make([]*big.Rat, 0, capHint)
	for rem.Deg() >= dd && !rem.IsZero() {
		s := rem.Deg() - dd
		c := new(big.Rat).Quo(rem.coeffs[len(rem.coeffs)-1], dlc)
		for len(qc) <= s {
			qc = append(qc, new(big.Rat))
		}
		qc[s] = new(big.Rat).Set(c)
		// rem -= c * x^s * d
		shift := make([]*big.Rat, s+len(d.coeffs))
		for i := range shift {
			shift[i] = new(big.Rat)
		}
		for i, dc := range d.coeffs {
			shift[s+i].Mul(c, dc)
		}
		rem = Sub(rem, Poly{coeffs: shift})
	}
	quo = Poly{coeffs: qc}.normalize()
	return quo, rem
}

// Content returns the content of p over Q: lcm of denominators divided out,
// i.e. the unique positive rational c and primitive integer polynomial P with
// p = c*P. It returns c and P (as integer coefficients).
func (p Poly) toPrimitiveZ() (alpha *big.Rat, ip []*big.Int) {
	if p.IsZero() {
		return new(big.Rat), nil
	}
	lcm := big.NewInt(1)
	for _, c := range p.coeffs {
		d := c.Denom()
		lcm.Mul(lcm, new(big.Int).Quo(d, igcd(lcm, d)))
	}
	ip = make([]*big.Int, len(p.coeffs))
	gcd := new(big.Int)
	for i, c := range p.coeffs {
		ip[i] = new(big.Int).Mul(c.Num(), new(big.Int).Quo(lcm, c.Denom()))
		gcd = igcd(gcd, ip[i])
	}
	if gcd.Sign() == 0 {
		gcd.SetInt64(1)
	}
	for i := range ip {
		ip[i].Quo(ip[i], gcd)
	}
	// Make the primitive part have positive leading coefficient.
	if ip[len(ip)-1].Sign() < 0 {
		for i := range ip {
			ip[i].Neg(ip[i])
		}
		gcd.Neg(gcd)
	}
	// p = (gcd/lcm) * P
	alpha = new(big.Rat).SetFrac(gcd, lcm)
	return alpha, ip
}

// fromPrimitiveZ converts an integer polynomial back to Poly.
func fromPrimitiveZ(ip []*big.Int) Poly {
	cs := make([]*big.Rat, len(ip))
	for i, c := range ip {
		cs[i] = new(big.Rat).SetInt(c)
	}
	return NewPoly(cs...)
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		c := p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		cs := c.RatString()
		switch i {
		case 0:
			terms = append(terms, cs)
		case 1:
			terms = append(terms, cs+"*x")
		default:
			terms = append(terms, fmt.Sprintf("%s*x^%d", cs, i))
		}
	}
	return strings.Join(terms, " + ")
}
