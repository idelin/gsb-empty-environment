package ratpoly

import "math/big"

// VerifyStats counts the coefficient-ring operations spent verifying a
// Bézout certificate.
type VerifyStats struct {
	MulOps int // coefficient multiplications
	AddOps int // coefficient additions/subtractions
}

// The verifier deliberately uses its own naive arithmetic (vAdd, vMul,
// vDivMod below) instead of the library's Add/Mul/DivMod, so the
// certificate check is an independent multiply-add computation.

func vAdd(a, b Poly, st *VerifyStats) Poly {
	n := len(a.c)
	if len(b.c) > n {
		n = len(b.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		ai, bi := a.Coeff(i), b.Coeff(i)
		c[i] = new(big.Rat).Add(ai, bi)
		st.AddOps++
	}
	return normalize(c)
}

func vNeg(a Poly) Poly {
	c := make([]*big.Rat, len(a.c))
	for i, x := range a.c {
		c[i] = new(big.Rat).Neg(x)
	}
	return Poly{c: c}
}

func vMul(a, b Poly, st *VerifyStats) Poly {
	if a.IsZero() || b.IsZero() {
		return Zero
	}
	c := make([]*big.Rat, len(a.c)+len(b.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, x := range a.c {
		for j, y := range b.c {
			t := new(big.Rat).Mul(x, y)
			st.MulOps++
			c[i+j].Add(c[i+j], t)
			st.AddOps++
		}
	}
	return normalize(c)
}

// vDivMod is a naive long division over Q used only by the verifier.
func vDivMod(a, b Poly, st *VerifyStats) (q, r Poly) {
	r = a
	q = Zero
	blc := b.LC()
	for !r.IsZero() && r.Degree() >= b.Degree() {
		s := new(big.Rat).Quo(r.LC(), blc)
		d := r.Degree() - b.Degree()
		mono := make([]*big.Rat, d+1)
		for i := range mono {
			mono[i] = new(big.Rat)
		}
		mono[d] = s
		t := NewPoly(mono...)
		q = vAdd(q, t, st)
		r = vAdd(r, vNeg(vMul(t, b, st)), st)
	}
	return q, r
}

// VerifyCertificate independently checks that (g, u, v) is a valid
// extended-GCD certificate for (a, b):
//
//  1. zero cases follow the documented conventions;
//  2. g is monic (or zero);
//  3. g divides both a and b (naive long division, zero remainder);
//  4. u*a + v*b == g (naive multiply-add).
//
// Together 3+4 imply g is a *greatest* common divisor: any common divisor
// of a and b divides u*a + v*b = g. Everything is checked symbolically
// with exact rational arithmetic — no evaluation at sample points.
func VerifyCertificate(a, b, g, u, v Poly) (bool, VerifyStats) {
	var st VerifyStats
	if g.IsZero() {
		// Only valid for a == b == 0, with the zero Bézout coefficients.
		return a.IsZero() && b.IsZero() && u.IsZero() && v.IsZero(), st
	}
	// Monic check.
	if g.LC().Cmp(big.NewRat(1, 1)) != 0 {
		return false, st
	}
	// Divisibility checks.
	if _, r := vDivMod(a, g, &st); !r.IsZero() {
		return false, st
	}
	if _, r := vDivMod(b, g, &st); !r.IsZero() {
		return false, st
	}
	// Bézout identity.
	lhs := vAdd(vMul(u, a, &st), vMul(v, b, &st), &st)
	diff := vAdd(lhs, vNeg(g), &st)
	return diff.IsZero(), st
}
