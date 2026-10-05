package algnum

import (
	"errors"
	"fmt"
	"math/big"
)

// MaxDegree is the maximum accepted polynomial degree.
const MaxDegree = 8

// Num is a real algebraic number: a non-zero polynomial together with an
// open isolating interval (Lo, Hi) with rational endpoints.
type Num struct {
	P  Poly
	Lo *big.Rat
	Hi *big.Rat
}

// Relation is the outcome of comparing two algebraic numbers.
type Relation int

const (
	Less Relation = iota
	Equal
	Greater
)

func (r Relation) String() string {
	switch r {
	case Less:
		return "less"
	case Equal:
		return "equal"
	case Greater:
		return "greater"
	}
	return "unknown"
}

// Certificate is an independently checkable proof of a comparison result.
//
// For "equal": GCD is a non-constant common divisor of both polynomials
// (ascending rational coefficients) and (CommonLo, CommonHi) is an
// interval inside both isolating intervals containing exactly one root of
// GCD; that root is then the unique root of each input.
//
// For "less"/"greater": (LhsLo, LhsHi) and (RhsLo, RhsHi) are refined
// isolating intervals, each contained in the corresponding input interval,
// that are disjoint and ordered, which forces the order of the roots.
type Certificate struct {
	Relation string   `json:"relation"`
	GCD      []string `json:"gcd,omitempty"`
	CommonLo string   `json:"commonLo,omitempty"`
	CommonHi string   `json:"commonHi,omitempty"`
	LhsLo    string   `json:"lhsLo,omitempty"`
	LhsHi    string   `json:"lhsHi,omitempty"`
	RhsLo    string   `json:"rhsLo,omitempty"`
	RhsHi    string   `json:"rhsHi,omitempty"`
}

// Validate checks that n is a legal algebraic-number representation:
// non-zero polynomial of degree 1..MaxDegree, Lo < Hi, endpoints are not
// roots, and the open interval contains exactly one distinct real root
// (counted via a Sturm sequence of the square-free part, so repeated
// factors are handled correctly).
func Validate(n Num) error {
	if n.P.IsZero() {
		return errors.New("polynomial must be non-zero")
	}
	if n.P.Deg() < 1 {
		return errors.New("polynomial must have degree >= 1")
	}
	if n.P.Deg() > MaxDegree {
		return fmt.Errorf("polynomial degree %d exceeds maximum %d", n.P.Deg(), MaxDegree)
	}
	if n.Lo == nil || n.Hi == nil {
		return errors.New("interval endpoints must be set")
	}
	if n.Lo.Cmp(n.Hi) >= 0 {
		return fmt.Errorf("invalid interval: require lo < hi, got %s and %s",
			n.Lo.RatString(), n.Hi.RatString())
	}
	if n.P.SignAt(n.Lo) == 0 {
		return fmt.Errorf("lower endpoint %s is a root of the polynomial", n.Lo.RatString())
	}
	if n.P.SignAt(n.Hi) == 0 {
		return fmt.Errorf("upper endpoint %s is a root of the polynomial", n.Hi.RatString())
	}
	st := NewSturmSeq(n.P.SquareFree())
	if got := st.CountRoots(n.Lo, n.Hi); got != 1 {
		return fmt.Errorf("interval (%s, %s) contains %d distinct real roots, not exactly one",
			n.Lo.RatString(), n.Hi.RatString(), got)
	}
	return nil
}

// refine halves the isolating interval of p around its unique root,
// keeping rational endpoints that are not roots. st must be the Sturm
// sequence of the square-free part of p.
func refine(p Poly, st SturmSeq, lo, hi *big.Rat) (*big.Rat, *big.Rat) {
	mid := new(big.Rat).Add(lo, hi)
	mid.Quo(mid, ratInt(2))
	if p.SignAt(mid) == 0 {
		// The midpoint is the (unique) root itself: shrink symmetrically.
		newLo := new(big.Rat).Add(lo, mid)
		newLo.Quo(newLo, ratInt(2))
		newHi := new(big.Rat).Add(mid, hi)
		newHi.Quo(newHi, ratInt(2))
		return newLo, newHi
	}
	if st.CountRoots(lo, mid) == 1 {
		return new(big.Rat).Set(lo), mid
	}
	return mid, new(big.Rat).Set(hi)
}

// Compare exactly compares two real algebraic numbers and returns the
// relation together with a checkable certificate. It returns an error if
// either input is not a valid isolating representation.
func Compare(a, b Num) (Relation, Certificate, error) {
	if err := Validate(a); err != nil {
		return 0, Certificate{}, fmt.Errorf("lhs: %w", err)
	}
	if err := Validate(b); err != nil {
		return 0, Certificate{}, fmt.Errorf("rhs: %w", err)
	}

	// Equality test: a non-constant gcd with a root in the intersection of
	// the two intervals means both intervals isolate that same root.
	lo := maxRat(a.Lo, b.Lo)
	hi := minRat(a.Hi, b.Hi)
	if lo.Cmp(hi) < 0 {
		g := GCD(a.P, b.P)
		if g.Deg() >= 1 {
			st := NewSturmSeq(g.SquareFree())
			if st.CountRoots(lo, hi) == 1 {
				return Equal, Certificate{
					Relation: Equal.String(),
					GCD:      RatStrings(g.Coeffs()),
					CommonLo: lo.RatString(),
					CommonHi: hi.RatString(),
				}, nil
			}
		}
	}

	// Distinct roots: refine both intervals until they are disjoint.
	sta := NewSturmSeq(a.P.SquareFree())
	stb := NewSturmSeq(b.P.SquareFree())
	aLo, aHi := new(big.Rat).Set(a.Lo), new(big.Rat).Set(a.Hi)
	bLo, bHi := new(big.Rat).Set(b.Lo), new(big.Rat).Set(b.Hi)
	for i := 0; ; i++ {
		if i > 100000 {
			return 0, Certificate{}, errors.New("interval refinement did not converge")
		}
		if aHi.Cmp(bLo) <= 0 {
			return Less, lessCert(aLo, aHi, bLo, bHi), nil
		}
		if bHi.Cmp(aLo) <= 0 {
			return Greater, greaterCert(aLo, aHi, bLo, bHi), nil
		}
		aLo, aHi = refine(a.P, sta, aLo, aHi)
		bLo, bHi = refine(b.P, stb, bLo, bHi)
	}
}

func lessCert(aLo, aHi, bLo, bHi *big.Rat) Certificate {
	return Certificate{
		Relation: Less.String(),
		LhsLo:    aLo.RatString(), LhsHi: aHi.RatString(),
		RhsLo: bLo.RatString(), RhsHi: bHi.RatString(),
	}
}

func greaterCert(aLo, aHi, bLo, bHi *big.Rat) Certificate {
	return Certificate{
		Relation: Greater.String(),
		LhsLo:    aLo.RatString(), LhsHi: aHi.RatString(),
		RhsLo: bLo.RatString(), RhsHi: bHi.RatString(),
	}
}

func maxRat(x, y *big.Rat) *big.Rat {
	if x.Cmp(y) >= 0 {
		return new(big.Rat).Set(x)
	}
	return new(big.Rat).Set(y)
}

func minRat(x, y *big.Rat) *big.Rat {
	if x.Cmp(y) <= 0 {
		return new(big.Rat).Set(x)
	}
	return new(big.Rat).Set(y)
}

func parseRat(s string) (*big.Rat, error) {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational %q", s)
	}
	return v, nil
}

// checkRefined verifies that (lo, hi) is an isolating interval for p
// contained in (origLo, origHi), hence isolating the same unique root.
func checkRefined(p Poly, origLo, origHi, lo, hi *big.Rat, who string) error {
	if lo.Cmp(hi) >= 0 {
		return fmt.Errorf("%s: refined interval is empty or inverted", who)
	}
	if lo.Cmp(origLo) < 0 || hi.Cmp(origHi) > 0 {
		return fmt.Errorf("%s: refined interval (%s, %s) is not inside the original (%s, %s)",
			who, lo.RatString(), hi.RatString(), origLo.RatString(), origHi.RatString())
	}
	if p.SignAt(lo) == 0 || p.SignAt(hi) == 0 {
		return fmt.Errorf("%s: refined interval endpoint is a root", who)
	}
	st := NewSturmSeq(p.SquareFree())
	if got := st.CountRoots(lo, hi); got != 1 {
		return fmt.Errorf("%s: refined interval contains %d distinct real roots, not exactly one", who, got)
	}
	return nil
}

// Verify independently checks that cert proves the claimed comparison
// result for a and b. It re-validates both inputs and every certificate
// statement from scratch using exact arithmetic; it never trusts the
// comparator. A nil error means the certificate is sound.
func Verify(a, b Num, cert Certificate) error {
	if err := Validate(a); err != nil {
		return fmt.Errorf("lhs input invalid: %w", err)
	}
	if err := Validate(b); err != nil {
		return fmt.Errorf("rhs input invalid: %w", err)
	}
	switch cert.Relation {
	case Equal.String():
		g, err := ParseRatPoly(cert.GCD)
		if err != nil {
			return fmt.Errorf("certificate gcd: %w", err)
		}
		if g.Deg() < 1 {
			return errors.New("certificate gcd must be a non-constant polynomial")
		}
		if _, ok := a.P.QuoExact(g); !ok {
			return errors.New("certificate gcd does not divide the lhs polynomial")
		}
		if _, ok := b.P.QuoExact(g); !ok {
			return errors.New("certificate gcd does not divide the rhs polynomial")
		}
		lo, err := parseRat(cert.CommonLo)
		if err != nil {
			return fmt.Errorf("certificate common interval: %w", err)
		}
		hi, err := parseRat(cert.CommonHi)
		if err != nil {
			return fmt.Errorf("certificate common interval: %w", err)
		}
		if lo.Cmp(hi) >= 0 {
			return errors.New("certificate common interval is empty or inverted")
		}
		if lo.Cmp(a.Lo) < 0 || hi.Cmp(a.Hi) > 0 {
			return errors.New("certificate common interval is not inside the lhs interval")
		}
		if lo.Cmp(b.Lo) < 0 || hi.Cmp(b.Hi) > 0 {
			return errors.New("certificate common interval is not inside the rhs interval")
		}
		if g.SignAt(lo) == 0 || g.SignAt(hi) == 0 {
			return errors.New("certificate common interval endpoint is a root of the gcd")
		}
		st := NewSturmSeq(g.SquareFree())
		if got := st.CountRoots(lo, hi); got != 1 {
			return fmt.Errorf("certificate gcd has %d distinct real roots in the common interval, not exactly one", got)
		}
		// The gcd's unique root in the common interval is a root of both
		// input polynomials inside both isolating intervals; since each
		// input interval isolates exactly one root, both numbers equal it.
		return nil
	case Less.String(), Greater.String():
		aLo, err := parseRat(cert.LhsLo)
		if err != nil {
			return fmt.Errorf("certificate lhs interval: %w", err)
		}
		aHi, err := parseRat(cert.LhsHi)
		if err != nil {
			return fmt.Errorf("certificate lhs interval: %w", err)
		}
		bLo, err := parseRat(cert.RhsLo)
		if err != nil {
			return fmt.Errorf("certificate rhs interval: %w", err)
		}
		bHi, err := parseRat(cert.RhsHi)
		if err != nil {
			return fmt.Errorf("certificate rhs interval: %w", err)
		}
		if err := checkRefined(a.P, a.Lo, a.Hi, aLo, aHi, "lhs"); err != nil {
			return err
		}
		if err := checkRefined(b.P, b.Lo, b.Hi, bLo, bHi, "rhs"); err != nil {
			return err
		}
		if cert.Relation == Less.String() {
			if aHi.Cmp(bLo) > 0 {
				return errors.New("certificate intervals are not disjoint in the claimed order (need lhsHi <= rhsLo)")
			}
		} else {
			if bHi.Cmp(aLo) > 0 {
				return errors.New("certificate intervals are not disjoint in the claimed order (need rhsHi <= lhsLo)")
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown certificate relation %q", cert.Relation)
	}
}
