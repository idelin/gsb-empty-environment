package ratsimp

import "math/big"

// rpoly is a univariate polynomial with rational coefficients, used
// internally for gcd, square-free factorization and Sturm sequences.
type rpoly struct {
	c []*big.Rat // c[i] = coefficient of x^i, normalized
}

func rnorm(c []*big.Rat) []*big.Rat {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return c[:n]
}

func rfromInts(p *Poly) rpoly {
	c := make([]*big.Rat, len(p.coeffs))
	for i, v := range p.coeffs {
		c[i] = new(big.Rat).SetInt(v)
	}
	return rpoly{c: rnorm(c)}
}

// toIntPoly converts a rational polynomial known to have integer
// coefficients up to a common denominator into an integer polynomial
// with content 1. The sign of the leading coefficient is preserved.
func (r rpoly) toIntPoly() *Poly {
	if r.isZero() {
		return &Poly{}
	}
	lcm := big.NewInt(1)
	g := new(big.Int)
	for _, v := range r.c {
		d := v.Denom()
		g.GCD(nil, nil, lcm, d)
		lcm.Quo(lcm, g)
		lcm.Mul(lcm, d)
	}
	c := make([]*big.Int, len(r.c))
	for i, v := range r.c {
		t := new(big.Rat).Mul(v, new(big.Rat).SetInt(lcm))
		c[i] = new(big.Int).Set(t.Num())
	}
	p := &Poly{coeffs: norm(c)}
	return p.divByInt(p.Content())
}

func (r rpoly) deg() int { return len(r.c) - 1 }

func (r rpoly) isZero() bool { return len(r.c) == 0 }

func (r rpoly) neg() rpoly {
	c := make([]*big.Rat, len(r.c))
	for i, v := range r.c {
		c[i] = new(big.Rat).Neg(v)
	}
	return rpoly{c: c}
}

func (r rpoly) sub(s rpoly) rpoly {
	n := len(r.c)
	if len(s.c) > n {
		n = len(s.c)
	}
	c := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		c[i] = new(big.Rat)
		if i < len(r.c) {
			c[i].Add(c[i], r.c[i])
		}
		if i < len(s.c) {
			c[i].Sub(c[i], s.c[i])
		}
	}
	return rpoly{c: rnorm(c)}
}

func (r rpoly) scale(t *big.Rat) rpoly {
	c := make([]*big.Rat, len(r.c))
	for i, v := range r.c {
		c[i] = new(big.Rat).Mul(v, t)
	}
	return rpoly{c: rnorm(c)}
}

// monic returns r divided by its leading coefficient.
func (r rpoly) monic() rpoly {
	if r.isZero() {
		return r
	}
	return r.scale(new(big.Rat).Inv(r.c[len(r.c)-1]))
}

func (r rpoly) deriv() rpoly {
	if len(r.c) <= 1 {
		return rpoly{}
	}
	c := make([]*big.Rat, len(r.c)-1)
	for i := 1; i < len(r.c); i++ {
		c[i-1] = new(big.Rat).Mul(r.c[i], new(big.Rat).SetInt64(int64(i)))
	}
	return rpoly{c: rnorm(c)}
}

// quoRem divides r by d over the rationals; d must be nonzero.
func (r rpoly) quoRem(d rpoly) (q, rem rpoly) {
	if d.isZero() {
		panic("ratsimp: division by zero polynomial")
	}
	rem = rpoly{c: append([]*big.Rat(nil), r.c...)}
	q = rpoly{}
	dd := d.deg()
	dlc := d.c[dd]
	for !rem.isZero() && rem.deg() >= dd {
		k := rem.deg() - dd
		t := new(big.Rat).Quo(rem.c[rem.deg()], dlc)
		for len(q.c) <= k {
			q.c = append(q.c, new(big.Rat))
		}
		q.c[k].Add(q.c[k], t)
		shift := make([]*big.Rat, k+len(d.c))
		for i := range shift {
			shift[i] = new(big.Rat)
			if i >= k {
				shift[i].Mul(t, d.c[i-k])
			}
		}
		rem = rem.sub(rpoly{c: shift})
	}
	q.c = rnorm(q.c)
	return q, rem
}

// rgcd returns the monic gcd of a and b.
func rgcd(a, b rpoly) rpoly {
	a = a.monic()
	b = b.monic()
	for !b.isZero() {
		_, rem := a.quoRem(b)
		a, b = b, rem.monic()
	}
	return a
}
