package main

import "math/big"

// Quad is a polynomial A*t^2 + B*t + C with rational coefficients.
// It represents the difference of two squared-distance functions.
type Quad struct{ A, B, C *big.Rat }

func ratInt(x int64) *big.Rat { return big.NewRat(x, 1) }

func ratStr(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return r.RatString()
}

func (q Quad) IsZero() bool {
	return q.A.Sign() == 0 && q.B.Sign() == 0 && q.C.Sign() == 0
}

// Eval evaluates q at a rational point, exactly.
func (q Quad) Eval(t *big.Rat) *big.Rat {
	r := new(big.Rat).Mul(q.A, t)
	r.Add(r, q.B)
	r.Mul(r, t)
	r.Add(r, q.C)
	return r
}

// Sub returns q - o.
func (q Quad) Sub(o Quad) Quad {
	return Quad{
		A: new(big.Rat).Sub(q.A, o.A),
		B: new(big.Rat).Sub(q.B, o.B),
		C: new(big.Rat).Sub(q.C, o.C),
	}
}

func discriminant(q Quad) *big.Rat {
	d := new(big.Rat).Mul(q.B, q.B)
	t := new(big.Rat).Mul(q.A, q.C)
	t.Mul(t, ratInt(4))
	return d.Sub(d, t)
}

// ratSqrt returns sqrt(x) if x is the square of a rational.
func ratSqrt(x *big.Rat) (*big.Rat, bool) {
	if x.Sign() < 0 {
		return nil, false
	}
	num, den := x.Num(), x.Denom()
	sn := new(big.Int).Sqrt(num)
	sd := new(big.Int).Sqrt(den)
	if new(big.Int).Mul(sn, sn).Cmp(num) == 0 && new(big.Int).Mul(sd, sd).Cmp(den) == 0 {
		return new(big.Rat).SetFrac(sn, sd), true
	}
	return nil, false
}

// squarefreePart writes n = k^2 * m with m squarefree. Trial division is
// adequate here because discriminants come from modest input coefficients.
func squarefreePart(n *big.Int) (m, k *big.Int) {
	m = big.NewInt(1)
	k = big.NewInt(1)
	rem := new(big.Int).Set(n)
	p := big.NewInt(2)
	pp := new(big.Int)
	quo, rem2 := new(big.Int), new(big.Int)
	for pp.Mul(p, p).Cmp(rem) <= 0 {
		e := 0
		for {
			quo.QuoRem(rem, p, rem2)
			if rem2.Sign() != 0 {
				break
			}
			rem.Set(quo)
			e++
		}
		if e > 0 {
			if e%2 == 1 {
				m.Mul(m, p)
			}
			for i := 0; i < e/2; i++ {
				k.Mul(k, p)
			}
		}
		p.Add(p, big.NewInt(1))
	}
	if rem.Cmp(big.NewInt(1)) > 0 { // rem is prime here
		m.Mul(m, rem)
	}
	return m, k
}

func inClosedInterval(r, T *big.Rat) bool {
	return r.Sign() >= 0 && r.Cmp(T) <= 0
}

// rootsOf returns every real root of q in [0, T], exactly. A root is either
// rational or an irrational algebraic number u + v*sqrt(m) carrying a
// rational isolating interval. A zero polynomial has no roots reported here;
// callers treat it as "always tied".
func rootsOf(q Quad, T *big.Rat) []*Root {
	if q.A.Sign() == 0 {
		if q.B.Sign() == 0 {
			return nil
		}
		r := new(big.Rat).Quo(new(big.Rat).Neg(q.C), q.B)
		if inClosedInterval(r, T) {
			return []*Root{RationalRoot(r)}
		}
		return nil
	}
	D := discriminant(q)
	if D.Sign() < 0 {
		return nil
	}
	twoA := new(big.Rat).Mul(ratInt(2), q.A)
	if D.Sign() == 0 {
		// Tangency: a double root. The order does not swap, but the two
		// objects are exactly tied at this instant, so it is still an event.
		r := new(big.Rat).Quo(new(big.Rat).Neg(q.B), twoA)
		if inClosedInterval(r, T) {
			return []*Root{RationalRoot(r)}
		}
		return nil
	}
	if s, ok := ratSqrt(D); ok {
		var out []*Root
		for _, sign := range []int64{-1, 1} {
			num := new(big.Rat).Neg(q.B)
			if sign > 0 {
				num.Add(num, s)
			} else {
				num.Sub(num, s)
			}
			r := num.Quo(num, twoA)
			if inClosedInterval(r, T) {
				out = append(out, RationalRoot(r))
			}
		}
		return out
	}
	// Two irrational conjugate roots u +/- |v|*sqrt(m).
	num, den := D.Num(), D.Denom()
	pq := new(big.Int).Mul(num, den)
	m, k := squarefreePart(pq)                         // sqrt(D) = k*sqrt(m)/den
	u := new(big.Rat).Quo(new(big.Rat).Neg(q.B), twoA) // vertex, rational
	vAbs := new(big.Rat).SetInt(k)
	vAbs.Quo(vAbs, new(big.Rat).Mul(twoA, new(big.Rat).SetInt(den)))
	if vAbs.Sign() < 0 {
		vAbs.Neg(vAbs)
	}
	signA := q.A.Sign()
	s0 := q.C.Sign()
	sT := q.Eval(T).Sign()
	if s0 == 0 || sT == 0 {
		panic("rootsOf: rational endpoint root with non-square discriminant")
	}
	var out []*Root
	mk := func(vSign int, lo, hi *big.Rat, loSign int) {
		v := new(big.Rat).Set(vAbs)
		if vSign < 0 {
			v.Neg(v)
		}
		out = append(out, &Root{u: u, v: v, m: m, poly: q, lo: lo, hi: hi, loSign: loSign})
	}
	// sign(f(vertex)) == -signA because D > 0.
	switch {
	case u.Sign() <= 0:
		// Left root is < vertex <= 0; only the right root can lie in (0, T).
		if s0*sT < 0 {
			mk(+1, ratInt(0), new(big.Rat).Set(T), s0)
		}
	case u.Cmp(T) >= 0:
		// Only the left root can lie in (0, T).
		if s0*sT < 0 {
			mk(-1, ratInt(0), new(big.Rat).Set(T), s0)
		}
	default:
		// Vertex strictly inside (0, T): each side may hold one root.
		if s0 == signA { // f(0) and f(vertex) have opposite signs
			mk(-1, ratInt(0), new(big.Rat).Set(u), s0)
		}
		if sT == signA { // f(vertex) and f(T) have opposite signs
			mk(+1, new(big.Rat).Set(u), new(big.Rat).Set(T), -signA)
		}
	}
	return out
}
