package polygcd

import "math/big"

// Stats records coefficient bit-length statistics gathered while computing
// an extended GCD. They quantify coefficient growth of the subresultant
// algorithm on a given input.
type Stats struct {
	// Steps is the number of pseudo-division steps in the remainder sequence.
	Steps int
	// MaxRemIntBits is the maximum bit length of any integer coefficient
	// appearing in the subresultant remainder sequence over Z.
	MaxRemIntBits int
	// MaxCofNumBits / MaxCofDenBits are the maximum bit lengths of numerators
	// and denominators of the (rational) Bezout cofactor coefficients
	// tracked during the remainder sequence.
	MaxCofNumBits int
	MaxCofDenBits int
	// ResultNumBits / ResultDenBits describe the final certificate
	// (G, S, T) coefficients.
	ResultNumBits int
	ResultDenBits int
}

// Certificate is the result of ExtendedGCD: a monic greatest common divisor
// G of A and B together with Bezout coefficients S, T such that
// S*A + T*B = G. Stats documents coefficient growth during the computation.
type Certificate struct {
	G, S, T Poly
	Stats   Stats
}

// ExtendedGCD computes the monic greatest common divisor G of a and b and
// Bezout coefficients S, T with S*a + T*b = G.
//
// Special cases:
//   - GCD(0, 0) = 0 with S = T = 0.
//   - GCD(a, 0) = monic(a) with S = 1/lc(a), T = 0 (and symmetrically).
//   - If either input is a nonzero constant, G = 1.
//
// The remainder sequence is computed with the subresultant PRS over Z
// (Geddes/Czapor/Labahn), which divides each pseudo-remainder by the exact
// factor g*h^delta; this keeps integer coefficients near the theoretical
// minimum instead of the exponential growth of a naive Euclidean PRS.
// Bezout cofactors undergo the identical recurrence and normalization.
func ExtendedGCD(a, b Poly) Certificate {
	var st Stats

	// Degenerate cases.
	if a.IsZero() && b.IsZero() {
		c := Certificate{G: Zero(), S: Zero(), T: Zero()}
		c.Stats = st
		return c
	}
	if b.IsZero() {
		s := Const(new(big.Rat).Inv(a.LC()))
		c := Certificate{G: a.Monic(), S: s, T: Zero()}
		st.updateResult(c)
		c.Stats = st
		return c
	}
	if a.IsZero() {
		t := Const(new(big.Rat).Inv(b.LC()))
		c := Certificate{G: b.Monic(), S: Zero(), T: t}
		st.updateResult(c)
		c.Stats = st
		return c
	}
	if a.IsConst() {
		// 1 = (1/a)*a + 0*b
		c := Certificate{G: One(), S: Const(new(big.Rat).Inv(a.LC())), T: Zero()}
		st.updateResult(c)
		c.Stats = st
		return c
	}
	if b.IsConst() {
		c := Certificate{G: One(), S: Zero(), T: Const(new(big.Rat).Inv(b.LC()))}
		st.updateResult(c)
		c.Stats = st
		return c
	}

	// Clear denominators and contents: a = alpha*A, b = beta*B with
	// A, B primitive integer polynomials with positive leading coefficients.
	alpha, A := a.toPrimitiveZ()
	beta, B := b.toPrimitiveZ()

	swapped := false
	if iDeg(A) < iDeg(B) {
		A, B = B, A
		alpha, beta = beta, alpha
		swapped = true
	}

	// Extended subresultant PRS.
	// Invariants: r0 = u0*A + v0*B, r1 = u1*A + v1*B over Q[x].
	r0, r1 := A, B
	u0, v0 := One(), Zero()
	u1, v1 := Zero(), One()
	gcoef := big.NewInt(1)
	h := big.NewInt(1)

	for !iIsZero(r1) {
		st.Steps++
		st.observeInts(r1)
		st.observeRats(u1)
		st.observeRats(v1)

		delta := iDeg(r0) - iDeg(r1)
		q, prem := iPseudoDivRem(r0, r1)
		// beta_i = gcoef * h^delta (exact divisor of prem over Z)
		betaI := new(big.Int).Exp(h, big.NewInt(int64(delta)), nil)
		betaI.Mul(betaI, gcoef)

		r2 := iQuoExact(prem, betaI)

		// lc(r1)^(delta+1) * r0 = q*r1 + prem, so the cofactors satisfy
		// the same linear recurrence; divide by betaI over Q.
		lcPow := new(big.Rat).SetInt(new(big.Int).Exp(
			r1[len(r1)-1], big.NewInt(int64(delta+1)), nil))
		betaQ := new(big.Rat).SetInt(betaI)
		qPoly := fromPrimitiveZ(q)

		u2 := scaleRat(Sub(scaleRat(u0, lcPow), Mul(qPoly, u1)), new(big.Rat).Inv(betaQ))
		v2 := scaleRat(Sub(scaleRat(v0, lcPow), Mul(qPoly, v1)), new(big.Rat).Inv(betaQ))

		// Subresultant update: g <- lc(r1), h <- g^delta / h^(delta-1).
		gcoef = new(big.Int).Set(r1[len(r1)-1])
		if delta == 0 {
			h = big.NewInt(1)
		} else {
			num := new(big.Int).Exp(gcoef, big.NewInt(int64(delta)), nil)
			den := new(big.Int).Exp(h, big.NewInt(int64(delta-1)), nil)
			h = num.Quo(num, den)
		}

		r0, u0, v0 = r1, u1, v1
		r1, u1, v1 = r2, u2, v2
	}
	st.observeInts(r0)
	st.observeRats(u0)
	st.observeRats(v0)

	// r0 = u0*A + v0*B is the (integer) gcd up to a unit.
	// Monic gcd over Q: G = r0 / lc(r0).
	// Since A = a/alpha and B = b/beta:
	//   G = u0/(lc*alpha) * a + v0/(lc*beta) * b
	lc := new(big.Rat).SetInt(r0[len(r0)-1])
	sden := new(big.Rat).Mul(lc, alpha)
	tden := new(big.Rat).Mul(lc, beta)
	S := scaleRat(u0, new(big.Rat).Inv(sden))
	T := scaleRat(v0, new(big.Rat).Inv(tden))
	if swapped {
		S, T = T, S
	}
	G := fromPrimitiveZ(r0).Monic()

	c := Certificate{G: G, S: S, T: T}
	st.updateResult(c)
	c.Stats = st
	return c
}

// GCD returns only the monic greatest common divisor of a and b.
func GCD(a, b Poly) Poly { return ExtendedGCD(a, b).G }

// Verify independently checks a certificate produced by ExtendedGCD using
// only plain multiply/add/divide arithmetic (no GCD machinery):
//  1. S*a + T*b == G (Bezout identity),
//  2. G divides a and G divides b (remainder zero),
//  3. G is monic (or zero iff both inputs are zero),
//  4. S = T = 0 in the zero case.
func Verify(a, b Poly, c Certificate) bool {
	if c.G.IsZero() {
		return a.IsZero() && b.IsZero() && c.S.IsZero() && c.T.IsZero()
	}
	if c.G.LC().Cmp(big.NewRat(1, 1)) != 0 {
		return false
	}
	lhs := Add(Mul(c.S, a), Mul(c.T, b))
	if !lhs.Equal(c.G) {
		return false
	}
	if _, r := DivMod(a, c.G); !r.IsZero() {
		return false
	}
	if _, r := DivMod(b, c.G); !r.IsZero() {
		return false
	}
	return true
}

// --- bit-length statistics -------------------------------------------------

func bitLenRat(r *big.Rat) (num, den int) {
	n := r.Num().BitLen()
	if r.Num().Sign() < 0 {
		n = new(big.Int).Neg(r.Num()).BitLen()
	}
	return n, r.Denom().BitLen()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (st *Stats) observeInts(p []*big.Int) {
	for _, c := range p {
		st.MaxRemIntBits = maxInt(st.MaxRemIntBits, c.BitLen())
	}
}

func (st *Stats) observeRats(p Poly) {
	for _, c := range p.coeffs {
		n, d := bitLenRat(c)
		st.MaxCofNumBits = maxInt(st.MaxCofNumBits, n)
		st.MaxCofDenBits = maxInt(st.MaxCofDenBits, d)
	}
}

func (st *Stats) updateResult(c Certificate) {
	for _, p := range []Poly{c.G, c.S, c.T} {
		for _, r := range p.coeffs {
			n, d := bitLenRat(r)
			st.ResultNumBits = maxInt(st.ResultNumBits, n)
			st.ResultDenBits = maxInt(st.ResultDenBits, d)
		}
	}
}

// --- integer polynomial helpers (coefficients low-to-high, normalized) -----

// igcd returns the (non-negative) greatest common divisor of a and b.
// It works for zero and negative inputs, unlike Int.GCD in older Go.
func igcd(a, b *big.Int) *big.Int {
	x := new(big.Int).Abs(a)
	y := new(big.Int).Abs(b)
	for y.Sign() != 0 {
		x, y = y, new(big.Int).Mod(x, y)
	}
	return x
}

func iDeg(p []*big.Int) int { return len(p) - 1 }

func iIsZero(p []*big.Int) bool { return len(p) == 0 }

func iNormalize(p []*big.Int) []*big.Int {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func iScale(p []*big.Int, c *big.Int) []*big.Int {
	out := make([]*big.Int, len(p))
	for i, x := range p {
		out[i] = new(big.Int).Mul(x, c)
	}
	return iNormalize(out)
}

func iShift(p []*big.Int, s int) []*big.Int {
	if len(p) == 0 {
		return nil
	}
	out := make([]*big.Int, len(p)+s)
	for i := range out {
		out[i] = new(big.Int)
	}
	copy(out[s:], p)
	return out
}

func iAdd(p, q []*big.Int) []*big.Int {
	n := len(p)
	if len(q) > n {
		n = len(q)
	}
	out := make([]*big.Int, n)
	for i := range out {
		out[i] = new(big.Int)
		if i < len(p) {
			out[i].Add(out[i], p[i])
		}
		if i < len(q) {
			out[i].Add(out[i], q[i])
		}
	}
	return iNormalize(out)
}

func iSub(p, q []*big.Int) []*big.Int {
	n := len(p)
	if len(q) > n {
		n = len(q)
	}
	out := make([]*big.Int, n)
	for i := range out {
		out[i] = new(big.Int)
		if i < len(p) {
			out[i].Add(out[i], p[i])
		}
		if i < len(q) {
			out[i].Sub(out[i], q[i])
		}
	}
	return iNormalize(out)
}

// iPseudoDivRem computes pseudo-quotient and pseudo-remainder:
// lc(g)^(deg(f)-deg(g)+1) * f = q*g + r with deg(r) < deg(g).
func iPseudoDivRem(f, g []*big.Int) (q, r []*big.Int) {
	dg := iDeg(g)
	r = append([]*big.Int(nil), f...)
	q = nil
	lcg := g[dg]
	for !iIsZero(r) && iDeg(r) >= dg {
		s := iDeg(r) - dg
		c := new(big.Int).Set(r[iDeg(r)])
		// q = q*lcg + c*x^s
		term := make([]*big.Int, s+1)
		for i := range term {
			term[i] = new(big.Int)
		}
		term[s] = c
		q = iAdd(iScale(q, lcg), term)
		// r = r*lcg - c*x^s*g
		r = iSub(iScale(r, lcg), iShift(iScale(g, c), s))
	}
	return q, r
}

// iQuoExact divides an integer polynomial by a nonzero integer exactly.
func iQuoExact(p []*big.Int, d *big.Int) []*big.Int {
	if len(p) == 0 {
		return nil
	}
	out := make([]*big.Int, len(p))
	rem := new(big.Int)
	for i, c := range p {
		out[i] = new(big.Int)
		out[i].QuoRem(c, d, rem)
		if rem.Sign() != 0 {
			panic("polygcd: subresultant division not exact (bug)")
		}
	}
	return iNormalize(out)
}

func scaleRat(p Poly, r *big.Rat) Poly { return p.Scale(r) }
