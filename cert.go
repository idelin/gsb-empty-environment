package algcmp

import (
	"fmt"
	"math/big"
)

// IntervalJSON is a rational open interval in JSON form.
type IntervalJSON struct {
	Lower string `json:"lower"`
	Upper string `json:"upper"`
}

// AlgJSON is the JSON description of one algebraic number: integer
// coefficients in ascending degree order plus a rational open
// isolating interval.
type AlgJSON struct {
	Poly  []string `json:"poly"`
	Lower string   `json:"lower"`
	Upper string   `json:"upper"`
}

// CertJSON is a checkable certificate for the result of a comparison.
//
// For "less"/"greater" it carries refined, disjoint isolating
// intervals.  For "equal" it carries a nonconstant common divisor of
// the two polynomials and a "meet" interval, contained in both
// isolating intervals, that contains a root of that divisor.
type CertJSON struct {
	Result string        `json:"result"` // "less", "equal" or "greater"
	Left   IntervalJSON  `json:"left"`
	Right  IntervalJSON  `json:"right"`
	GCD    []string      `json:"gcd,omitempty"`
	Meet   *IntervalJSON `json:"meet,omitempty"`
}

// Doc is the top-level JSON document accepted by the CLI: a pair of
// algebraic numbers plus (for verification) a certificate.
type Doc struct {
	Left        AlgJSON   `json:"left"`
	Right       AlgJSON   `json:"right"`
	Certificate *CertJSON `json:"certificate,omitempty"`
}

// AlgNum parses and validates the JSON description.
func (a AlgJSON) AlgNum() (AlgNum, error) {
	p, err := ParsePoly(a.Poly)
	if err != nil {
		return AlgNum{}, err
	}
	l, err := parseRat(a.Lower)
	if err != nil {
		return AlgNum{}, fmt.Errorf("lower endpoint: %v", err)
	}
	r, err := parseRat(a.Upper)
	if err != nil {
		return AlgNum{}, fmt.Errorf("upper endpoint: %v", err)
	}
	return NewAlgNum(p, l, r)
}

func parseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("%q is not a valid rational number", s)
	}
	return r, nil
}

func parseInterval(iv IntervalJSON) (*big.Rat, *big.Rat, error) {
	l, err := parseRat(iv.Lower)
	if err != nil {
		return nil, nil, fmt.Errorf("lower endpoint: %v", err)
	}
	r, err := parseRat(iv.Upper)
	if err != nil {
		return nil, nil, fmt.Errorf("upper endpoint: %v", err)
	}
	return l, r, nil
}

// CompareJSON validates both inputs and compares them, returning the
// result word and a certificate.
func CompareJSON(left, right AlgJSON) (string, *CertJSON, error) {
	a, err := left.AlgNum()
	if err != nil {
		return "", nil, fmt.Errorf("left: %v", err)
	}
	b, err := right.AlgNum()
	if err != nil {
		return "", nil, fmt.Errorf("right: %v", err)
	}
	res, cert := Compare(a, b)
	word := map[int]string{-1: "less", 0: "equal", 1: "greater"}[res]
	if cert.Result != word {
		panic("internal: certificate/result mismatch")
	}
	return word, cert, nil
}

// Verify independently checks a certificate against the comparison
// inputs.  It re-validates both isolating intervals from scratch and
// then checks the certificate's claim using only rational arithmetic,
// polynomial GCD/division and Sturm root counts.  A nil error means the
// certificate proves the stated result.
func Verify(doc Doc) error {
	if doc.Certificate == nil {
		return fmt.Errorf("no certificate provided")
	}
	cert := doc.Certificate
	a, err := doc.Left.AlgNum()
	if err != nil {
		return fmt.Errorf("left input: %v", err)
	}
	b, err := doc.Right.AlgNum()
	if err != nil {
		return fmt.Errorf("right input: %v", err)
	}
	ca, err := certifiedSubInterval("left", a, cert.Left)
	if err != nil {
		return err
	}
	cb, err := certifiedSubInterval("right", b, cert.Right)
	if err != nil {
		return err
	}
	switch cert.Result {
	case "less":
		if ca.R.Cmp(cb.L) > 0 {
			return fmt.Errorf("certificate invalid: left interval (%s, %s) is not entirely below right interval (%s, %s)",
				ca.L.RatString(), ca.R.RatString(), cb.L.RatString(), cb.R.RatString())
		}
	case "greater":
		if cb.R.Cmp(ca.L) > 0 {
			return fmt.Errorf("certificate invalid: right interval (%s, %s) is not entirely below left interval (%s, %s)",
				cb.L.RatString(), cb.R.RatString(), ca.L.RatString(), ca.R.RatString())
		}
	case "equal":
		return verifyEqual(a, b, ca, cb, cert)
	default:
		return fmt.Errorf("certificate invalid: unknown result %q", cert.Result)
	}
	return nil
}

// certifiedSubInterval parses the certificate interval and checks that
// it is a valid isolating interval for the same root as n: it must lie
// inside n's interval and contain exactly one root.
func certifiedSubInterval(side string, n AlgNum, iv IntervalJSON) (AlgNum, error) {
	l, r, err := parseInterval(iv)
	if err != nil {
		return AlgNum{}, fmt.Errorf("%s certificate interval: %v", side, err)
	}
	if n.L.Cmp(l) > 0 || r.Cmp(n.R) > 0 {
		return AlgNum{}, fmt.Errorf("%s certificate interval (%s, %s) is not contained in the input interval (%s, %s)",
			side, l.RatString(), r.RatString(), n.L.RatString(), n.R.RatString())
	}
	m, err := NewAlgNum(n.P, l, r)
	if err != nil {
		return AlgNum{}, fmt.Errorf("%s certificate interval: %v", side, err)
	}
	return m, nil
}

// verifyEqual checks an equality certificate: a nonconstant common
// divisor g of both (square-free) polynomials with a root in a "meet"
// interval that lies inside both certified isolating intervals.  That
// root is a root of both polynomials inside their isolating intervals,
// hence equals both algebraic numbers.
func verifyEqual(a, b, ca, cb AlgNum, cert *CertJSON) error {
	if len(cert.GCD) == 0 {
		return fmt.Errorf("equality certificate missing gcd polynomial")
	}
	if cert.Meet == nil {
		return fmt.Errorf("equality certificate missing meet interval")
	}
	g, err := ParsePoly(cert.GCD)
	if err != nil {
		return fmt.Errorf("equality certificate gcd: %v", err)
	}
	if g.Deg() < 1 {
		return fmt.Errorf("equality certificate gcd must be nonconstant")
	}
	if _, rem := DivMod(a.P, g); !rem.IsZero() {
		return fmt.Errorf("equality certificate invalid: gcd does not divide the left polynomial")
	}
	if _, rem := DivMod(b.P, g); !rem.IsZero() {
		return fmt.Errorf("equality certificate invalid: gcd does not divide the right polynomial")
	}
	lo, hi, err := parseInterval(*cert.Meet)
	if err != nil {
		return fmt.Errorf("equality certificate meet interval: %v", err)
	}
	if lo.Cmp(hi) >= 0 {
		return fmt.Errorf("equality certificate invalid: meet interval is empty")
	}
	if ca.L.Cmp(lo) > 0 || hi.Cmp(ca.R) > 0 || cb.L.Cmp(lo) > 0 || hi.Cmp(cb.R) > 0 {
		return fmt.Errorf("equality certificate invalid: meet interval (%s, %s) is not contained in both certified intervals",
			lo.RatString(), hi.RatString())
	}
	if g.SignAt(lo) == 0 || g.SignAt(hi) == 0 {
		return fmt.Errorf("equality certificate invalid: meet interval endpoint is a root of the gcd")
	}
	if CountRoots(SquareFree(g), lo, hi) < 1 {
		return fmt.Errorf("equality certificate invalid: gcd has no root in the meet interval")
	}
	return nil
}
