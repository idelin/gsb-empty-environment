package ratsimp

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Rational is a rational function Num/Den with integer polynomial
// coefficients. Den must be nonzero.
type Rational struct {
	Num, Den *Poly
}

// NewRational builds the rational function num/den. It returns an
// error when den is the zero polynomial; a nil num means zero.
func NewRational(num, den *Poly) (*Rational, error) {
	if den == nil || den.IsZero() {
		return nil, errors.New("ratsimp: zero denominator polynomial")
	}
	if num == nil {
		num = &Poly{}
	}
	return &Rational{Num: num, Den: den}, nil
}

// PointKind classifies a point excluded from the domain.
type PointKind int

const (
	// Hole is a removable discontinuity: the factor canceled during
	// simplification, but the original function is still undefined
	// at the point.
	Hole PointKind = iota
	// Pole is a non-removable singularity that survives
	// simplification.
	Pole
)

func (k PointKind) String() string {
	if k == Hole {
		return "hole"
	}
	return "pole"
}

// ExcludedPoint describes one x-value excluded from the domain of the
// original (unsimplified) rational function.
type ExcludedPoint struct {
	// Where exactly isolates the excluded x-value.
	Where Interval
	// Kind is Hole when the discontinuity is removable, Pole
	// otherwise.
	Kind PointKind
	// OriginalMultiplicity is the multiplicity of the point as a
	// root of the original denominator.
	OriginalMultiplicity int
	// PoleOrder is the multiplicity that remains in the reduced
	// denominator; it is 0 exactly for holes.
	PoleOrder int

	factor rpoly // square-free factor having this root; for Refine
}

// Refine narrows the isolating interval of the point to width at most
// maxWidth, keeping exact rational endpoints.
func (e ExcludedPoint) Refine(maxWidth *big.Rat) Interval {
	return refineInterval(e.factor, e.Where, maxWidth)
}

func (e ExcludedPoint) String() string {
	if e.Kind == Hole {
		return fmt.Sprintf("x in %s: hole (original multiplicity %d)",
			e.Where, e.OriginalMultiplicity)
	}
	return fmt.Sprintf("x in %s: pole of order %d (original multiplicity %d)",
		e.Where, e.PoleOrder, e.OriginalMultiplicity)
}

// Simplified is the result of simplifying a rational function while
// preserving the domain of the original expression.
type Simplified struct {
	// Num and Den are the reduced numerator and denominator:
	// coprime integer polynomials, Den with positive leading
	// coefficient and no shared integer content with Num.
	Num, Den *Poly
	// Excluded lists every point excluded from the original domain,
	// in increasing order, including removable holes.
	Excluded []ExcludedPoint
}

func (s *Simplified) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "reduced: (%s) / (%s)\n", s.Num, s.Den)
	if len(s.Excluded) == 0 {
		b.WriteString("domain: all real numbers\n")
	} else {
		b.WriteString("excluded from original domain:\n")
		for _, e := range s.Excluded {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return b.String()
}

// Simplify reduces r to lowest terms. Canceling a factor does not
// remove the corresponding points from the recorded domain: they are
// reported as holes (removable discontinuities) together with the
// surviving poles and their orders.
func Simplify(r *Rational) *Simplified {
	g := r.Num.GCD(r.Den)
	num := r.Num.ExactDiv(g)
	den := r.Den.ExactDiv(g)

	// Remove any remaining common integer content and fix the sign.
	if num.IsZero() {
		den = PolyFromInts(1)
	} else {
		cg := new(big.Int).GCD(nil, nil, num.Content(), den.Content())
		if cg.Cmp(big.NewInt(1)) > 0 {
			num = num.divByInt(cg)
			den = den.divByInt(cg)
		}
		if den.Leading().Sign() < 0 {
			num = num.Neg()
			den = den.Neg()
		}
	}

	denSq := squareFree(rfromInts(r.Den))
	gSq := squareFree(rfromInts(g))

	type multFactor struct {
		mult int
		seq  []rpoly
	}
	var denFactors []multFactor
	for _, df := range denSq {
		denFactors = append(denFactors, multFactor{df.mult, sturmSeq(df.f)})
	}
	var gFactors []multFactor
	for _, gf := range gSq {
		gFactors = append(gFactors, multFactor{gf.mult, sturmSeq(gf.f)})
	}

	// Isolate the roots of the square-free part of the original
	// denominator: every interval contains exactly one excluded
	// point, and intervals are pairwise separated.
	denR := rfromInts(r.Den)
	sf := denR
	if denR.deg() >= 1 {
		sf, _ = denR.quoRem(rgcd(denR, denR.deriv()))
	}
	var excluded []ExcludedPoint
	for _, iv := range isolateRoots(sf) {
		multDen := 0
		for _, df := range denFactors {
			if countRoots(df.seq, iv.Lo, iv.Hi) > 0 {
				multDen = df.mult
				break
			}
		}
		multG := 0
		for _, gf := range gFactors {
			if countRoots(gf.seq, iv.Lo, iv.Hi) > 0 {
				multG = gf.mult
				break
			}
		}
		order := multDen - multG
		pt := ExcludedPoint{
			Where:                iv,
			OriginalMultiplicity: multDen,
			PoleOrder:            order,
			factor:               sf,
		}
		if order == 0 {
			pt.Kind = Hole
		} else {
			pt.Kind = Pole
		}
		excluded = append(excluded, pt)
	}
	return &Simplified{Num: num, Den: den, Excluded: excluded}
}

// EquivMode selects how two rational functions are compared.
type EquivMode int

const (
	// SameDomain requires identical domains (the same excluded
	// points) and equal values on that domain.
	SameDomain EquivMode = iota
	// CommonDomain only requires equal values wherever both
	// functions are defined; one function may have holes where the
	// other is defined.
	CommonDomain
)

// Equivalent reports whether a and b are equivalent under the given
// mode.
func Equivalent(a, b *Rational, mode EquivMode) bool {
	sa, sb := Simplify(a), Simplify(b)
	if !sa.Num.Mul(sb.Den).Equal(sb.Num.Mul(sa.Den)) {
		return false
	}
	if mode == CommonDomain {
		return true
	}
	return sameRealRoots(a.Den, b.Den)
}

// sameRealRoots reports whether p and q vanish at exactly the same
// real points.
func sameRealRoots(p, q *Poly) bool {
	sfP, sfQ := sqfreePart(p), sqfreePart(q)
	g := sfP.GCD(sfQ)
	return realRootCount(sfP.ExactDiv(g)) == 0 && realRootCount(sfQ.ExactDiv(g)) == 0
}

// sqfreePart returns p divided by gcd(p, p'): a square-free polynomial
// with the same distinct roots as p.
func sqfreePart(p *Poly) *Poly {
	rp := rfromInts(p)
	if rp.deg() < 1 {
		return p.Primitive()
	}
	sf, _ := rp.quoRem(rgcd(rp, rp.deriv()))
	return sf.toIntPoly()
}

// realRootCount counts the distinct real roots of p.
func realRootCount(p *Poly) int {
	rp := rfromInts(p)
	if rp.deg() < 1 {
		return 0
	}
	// The square-free part has the same distinct roots.
	sf, _ := rp.quoRem(rgcd(rp, rp.deriv()))
	seq := sturmSeq(sf)
	m := rootBound(sf)
	return countRoots(seq, new(big.Rat).Neg(m), m)
}
