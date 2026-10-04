package ratbezier

import (
	"errors"
	"math/big"
)

// Curve is a 1D rational Bezier curve: control values Y[i] with weights W[i].
//
//	y(t) = ( sum_i W[i]*Y[i]*B_i(t) ) / ( sum_i W[i]*B_i(t) )
//
// where B_i is the i-th Bernstein basis polynomial of degree n = len(Y)-1.
type Curve struct {
	Y []*big.Rat
	W []*big.Rat
}

// NewCurve validates and copies the given control values and weights.
func NewCurve(y, w []*big.Rat) (*Curve, error) {
	if len(y) != len(w) {
		return nil, errors.New("ratbezier: control points and weights must have equal length")
	}
	if len(y) < 2 {
		return nil, errors.New("ratbezier: need at least 2 control points")
	}
	c := &Curve{Y: make([]*big.Rat, len(y)), W: make([]*big.Rat, len(w))}
	for i := range y {
		if y[i] == nil || w[i] == nil {
			return nil, errors.New("ratbezier: nil control value or weight")
		}
		if w[i].Sign() == 0 {
			return nil, errors.New("ratbezier: weights must be nonzero")
		}
		c.Y[i] = new(big.Rat).Set(y[i])
		c.W[i] = new(big.Rat).Set(w[i])
	}
	return c, nil
}

// bernstein returns the i-th Bernstein basis polynomial of degree n:
// B_i(t) = C(n,i) * t^i * (1-t)^(n-i).
func bernstein(n, i int) *Poly {
	// (1-t)
	oneMinusT := NewPoly(1, -1)
	// t
	tPoly := NewPoly(0, 1)
	res := NewPoly(1)
	for k := 0; k < i; k++ {
		res = res.Mul(tPoly)
	}
	for k := 0; k < n-i; k++ {
		res = res.Mul(oneMinusT)
	}
	return res.Scale(rat(binomial(n, i)))
}

func binomial(n, k int) int64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := int64(1)
	for i := 0; i < k; i++ {
		r = r * int64(n-i) / int64(i+1)
	}
	return r
}

// NumeratorPoly returns N(t) = sum_i W[i]*Y[i]*B_i(t).
func (c *Curve) NumeratorPoly() *Poly {
	n := len(c.Y) - 1
	res := &Poly{}
	for i := range c.Y {
		coeff := new(big.Rat).Mul(c.W[i], c.Y[i])
		res = res.Add(bernstein(n, i).Scale(coeff))
	}
	return res
}

// DenominatorPoly returns D(t) = sum_i W[i]*B_i(t).
func (c *Curve) DenominatorPoly() *Poly {
	n := len(c.W) - 1
	res := &Poly{}
	for i := range c.W {
		res = res.Add(bernstein(n, i).Scale(c.W[i]))
	}
	return res
}

// DerivativePoly returns the numerator of y'(t):
//
//	P(t) = N'(t)*D(t) - N(t)*D'(t)
//
// y'(t) = P(t) / D(t)^2, so on any interval where D has no zero (in
// particular [0,1] for positive weights) the sign of y' equals the sign of P,
// and the extrema of y are exactly the roots of P plus the endpoints.
func (c *Curve) DerivativePoly() *Poly {
	n := c.NumeratorPoly()
	d := c.DenominatorPoly()
	return n.Derivative().Mul(d).Add(n.Mul(d.Derivative()).Neg())
}

// Eval evaluates the curve y(t) exactly at a rational parameter t.
func (c *Curve) Eval(t *big.Rat) *big.Rat {
	num := c.NumeratorPoly().Eval(t)
	den := c.DenominatorPoly().Eval(t)
	return new(big.Rat).Quo(num, den)
}
