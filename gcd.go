package ratpoly

import (
	"errors"
	"math/big"
)

// ErrVerificationFailed is returned when the internally verified Bézout
// certificate does not check out (it indicates an implementation bug).
var ErrVerificationFailed = errors.New("ratpoly: Bézout certificate verification failed")

// Stats collects coefficient bit-length statistics of a GCD run.
type Stats struct {
	// Steps is the number of subresultant PRS iterations.
	Steps int
	// RemBits[i] is the max coefficient bit length of the i-th remainder.
	RemBits []int
	// MaxRemBits is the largest coefficient bit length seen among remainders.
	MaxRemBits int
	// MaxCofactorBits is the largest coefficient bit length seen among
	// the intermediate Bézout cofactors.
	MaxCofactorBits int
	// Verified reports whether the returned certificate passed the
	// independent multiply-add verification.
	Verified bool
	// VerifyMulOps / VerifyAddOps count coefficient multiplications and
	// additions spent in the independent certificate verification.
	VerifyMulOps int
	VerifyAddOps int
}

// GCD returns the monic greatest common divisor of a and b over Q.
// gcd(0,0) = 0; gcd(0,f) = gcd(f,0) = Monic(f).
func GCD(a, b Poly) Poly {
	g, _, _, _, _ := ExtendedGCD(a, b)
	return g
}

// ExtendedGCD returns the monic g = gcd(a,b) and Bézout coefficients u, v
// with u*a + v*b = g, together with bit-length statistics.
//
// The computation is fraction-free: denominators are cleared once, then a
// subresultant polynomial remainder sequence over Z keeps intermediate
// coefficients near the subresultant bound instead of the exponential
// growth of naive Euclidean division over Q. Bézout cofactors are carried
// through the same pseudo-division steps (extended subresultant scheme).
//
// Edge cases: gcd(0,0) = 0 with u = v = 0; gcd(0,f) = Monic(f); the gcd of
// two nonzero constants is 1. The returned certificate is checked with an
// independent multiply-add verifier before returning.
func ExtendedGCD(a, b Poly) (g, u, v Poly, st Stats, err error) {
	switch {
	case a.IsZero() && b.IsZero():
		st.Verified = true
		return Zero, Zero, Zero, st, nil
	case a.IsZero():
		g, u, v = b.Monic(), Zero, NewPoly(new(big.Rat).Inv(b.LC()))
	case b.IsZero():
		g, u, v = a.Monic(), NewPoly(new(big.Rat).Inv(a.LC())), Zero
	default:
		g, u, v = extGCDNonzero(a, b, &st)
	}
	ok, vs := VerifyCertificate(a, b, g, u, v)
	st.Verified = ok
	st.VerifyMulOps = vs.MulOps
	st.VerifyAddOps = vs.AddOps
	if !ok {
		return g, u, v, st, ErrVerificationFailed
	}
	return g, u, v, st, nil
}

// extGCDNonzero handles the case a,b both nonzero.
func extGCDNonzero(a, b Poly, st *Stats) (g, u, v Poly) {
	ai, da := toIntPoly(a)
	bi, db := toIntPoly(b)
	swapped := false
	if ideg(ai) < ideg(bi) {
		ai, bi = bi, ai
		da, db = db, da
		swapped = true
	}
	gi, ui, vi := subresExt(ai, bi, st)
	// gi = ui*ai + vi*bi over Z, with ai = da*a and bi = db*b.
	// Monic g = gi / lc(gi), hence u = ui*da/lc(gi), v = vi*db/lc(gi).
	lc := gi[len(gi)-1]
	su := new(big.Rat).SetInt(da)
	su.Quo(su, new(big.Rat).SetInt(lc))
	sv := new(big.Rat).SetInt(db)
	sv.Quo(sv, new(big.Rat).SetInt(lc))
	g = ipolyToPoly(gi, new(big.Rat).Quo(big.NewRat(1, 1), new(big.Rat).SetInt(lc)))
	u = ipolyToPoly(ui, su)
	v = ipolyToPoly(vi, sv)
	if swapped {
		u, v = v, u
	}
	return g, u, v
}

// subresExt runs the extended subresultant PRS over Z.
// Requires deg(a) >= deg(b) >= 0 and b != 0. It returns the last nonzero
// remainder g (an integer multiple of the primitive gcd) and cofactors
// u, v with u*a + v*b = g, all integer polynomials.
func subresExt(a, b ipoly, st *Stats) (g, u, v ipoly) {
	if ideg(b) == 0 {
		return icopy(b), nil, ipoly{big.NewInt(1)}
	}
	R, S := a, b
	UR, US := ipoly{big.NewInt(1)}, ipoly(nil)
	VR, VS := ipoly(nil), ipoly{big.NewInt(1)}
	gg := big.NewInt(1)
	h := big.NewInt(1)
	record := func(p ipoly, cofactor bool) {
		n := imaxBitLen(p)
		if cofactor {
			if n > st.MaxCofactorBits {
				st.MaxCofactorBits = n
			}
			return
		}
		st.RemBits = append(st.RemBits, n)
		if n > st.MaxRemBits {
			st.MaxRemBits = n
		}
	}
	record(R, false)
	record(S, false)
	for ideg(S) > 0 {
		delta := ideg(R) - ideg(S)
		q, prem := pseudoDivRem(R, S)
		if len(prem) == 0 {
			return S, US, VS
		}
		// beta = (-1)^(delta+1) * gg * h^delta
		beta := new(big.Int).Exp(h, big.NewInt(int64(delta)), nil)
		beta.Mul(beta, gg)
		if delta%2 == 0 {
			beta.Neg(beta)
		}
		// prem = lc(S)^(delta+1)*R - q*S; mirror the combination on cofactors.
		pow := new(big.Int).Exp(S[len(S)-1], big.NewInt(int64(delta+1)), nil)
		UP := isub(iscale(UR, pow), imul(q, US))
		VP := isub(iscale(VR, pow), imul(q, VS))
		R, UR, VR = S, US, VS
		S = iexactDiv(prem, beta)
		US = iexactDiv(UP, beta)
		VS = iexactDiv(VP, beta)
		// g,h bookkeeping (Geddes/Czapor/Labahn subresultant PRS).
		gg.Set(R[len(R)-1])
		if delta > 0 {
			num := new(big.Int).Exp(gg, big.NewInt(int64(delta)), nil)
			if delta > 1 {
				den := new(big.Int).Exp(h, big.NewInt(int64(delta-1)), nil)
				rem := new(big.Int)
				num.QuoRem(num, den, rem)
				if rem.Sign() != 0 {
					panic("ratpoly: inexact subresultant h update")
				}
			}
			h.Set(num)
		}
		st.Steps++
		record(S, false)
		record(US, true)
		record(VS, true)
	}
	return S, US, VS
}
