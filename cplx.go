// Package cpoly implements sparse polynomials over the complex numbers
// with rational real and imaginary parts.
package cpoly

import (
	"math/big"
	"strings"
)

// Cplx is a complex number with rational real and imaginary parts.
// It is a value type; the internal *big.Rat fields are never shared
// with caller-visible state because all constructors and accessors
// deep-copy them.
type Cplx struct {
	Re *big.Rat
	Im *big.Rat
}

// NewCplx builds a Cplx from the given rationals, copying them.
// A nil argument is treated as zero.
func NewCplx(re, im *big.Rat) Cplx {
	return Cplx{Re: copyRat(re), Im: copyRat(im)}
}

// NewCplxInt builds a Cplx from integers.
func NewCplxInt(re, im int64) Cplx {
	return Cplx{Re: big.NewRat(re, 1), Im: big.NewRat(im, 1)}
}

// ZeroCplx is the additive identity.
func ZeroCplx() Cplx { return NewCplxInt(0, 0) }

func copyRat(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(r)
}

// Copy returns a deep copy of c.
func (c Cplx) Copy() Cplx { return NewCplx(c.Re, c.Im) }

// IsZero reports whether c == 0.
func (c Cplx) IsZero() bool { return c.Re.Sign() == 0 && c.Im.Sign() == 0 }

// IsReal reports whether the imaginary part vanishes.
func (c Cplx) IsReal() bool { return c.Im.Sign() == 0 }

// Equal reports exact equality.
func (c Cplx) Equal(o Cplx) bool { return c.Re.Cmp(o.Re) == 0 && c.Im.Cmp(o.Im) == 0 }

// Add returns c + o.
func (c Cplx) Add(o Cplx) Cplx {
	return Cplx{
		Re: new(big.Rat).Add(c.Re, o.Re),
		Im: new(big.Rat).Add(c.Im, o.Im),
	}
}

// Sub returns c - o.
func (c Cplx) Sub(o Cplx) Cplx {
	return Cplx{
		Re: new(big.Rat).Sub(c.Re, o.Re),
		Im: new(big.Rat).Sub(c.Im, o.Im),
	}
}

// Neg returns -c.
func (c Cplx) Neg() Cplx {
	return Cplx{
		Re: new(big.Rat).Neg(c.Re),
		Im: new(big.Rat).Neg(c.Im),
	}
}

// Mul returns c * o.
func (c Cplx) Mul(o Cplx) Cplx {
	ac := new(big.Rat).Mul(c.Re, o.Re)
	bd := new(big.Rat).Mul(c.Im, o.Im)
	ad := new(big.Rat).Mul(c.Re, o.Im)
	bc := new(big.Rat).Mul(c.Im, o.Re)
	return Cplx{
		Re: ac.Sub(ac, bd),
		Im: ad.Add(ad, bc),
	}
}

// Conj returns the complex conjugate of c.
func (c Cplx) Conj() Cplx {
	return Cplx{Re: copyRat(c.Re), Im: new(big.Rat).Neg(c.Im)}
}

// AbsSq returns |c|^2 as a rational.
func (c Cplx) AbsSq() *big.Rat {
	r := new(big.Rat).Mul(c.Re, c.Re)
	return r.Add(r, new(big.Rat).Mul(c.Im, c.Im))
}

// String renders c in lowest terms, e.g. "3/2", "-2i", "1/2 + 3/4i".
func (c Cplx) String() string {
	reZero := c.Re.Sign() == 0
	imZero := c.Im.Sign() == 0
	switch {
	case reZero && imZero:
		return "0"
	case imZero:
		return c.Re.RatString()
	case reZero:
		return ratWithI(c.Im)
	default:
		if c.Im.Sign() < 0 {
			neg := new(big.Rat).Neg(c.Im)
			return c.Re.RatString() + " - " + ratWithI(neg)
		}
		return c.Re.RatString() + " + " + ratWithI(c.Im)
	}
}

func ratWithI(r *big.Rat) string {
	if r.Cmp(big.NewRat(1, 1)) == 0 {
		return "i"
	}
	if r.Cmp(big.NewRat(-1, 1)) == 0 {
		return "-i"
	}
	return r.RatString() + "i"
}

// needsParens reports whether the rendered coefficient contains a
// top-level sum and must be parenthesised inside a polynomial term.
func (c Cplx) needsParens() bool {
	return strings.Contains(c.String(), " + ") || strings.Contains(c.String(), " - ")
}
