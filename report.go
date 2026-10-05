package main

import (
	"errors"
	"fmt"
	"math/big"
)

// Evidence is one isolated root together with its exact sign-change witness.
// For interval roots, LowerVal and UpperVal are the square-free polynomial's
// exact values at the bounds and have opposite signs.
type Evidence struct {
	RootIsolation
	LowerVal, UpperVal *big.Rat
}

// Report is the full analysis of a polynomial on a rational open interval.
type Report struct {
	Original   Poly
	SquareFree Poly
	A, B       *big.Rat
	RootAtA    bool // a itself is a root (reported separately, not counted)
	RootAtB    bool // b itself is a root (reported separately, not counted)
	Count      int  // number of distinct real roots in the open interval (a, b)
	Roots      []Evidence
}

// Analyze counts and isolates the distinct real roots of p in the open
// interval (a, b). Multiple roots are deduplicated by replacing p with its
// square-free part p / gcd(p, p'); all sign evidence is evaluated on that
// square-free part. The zero polynomial is rejected. Everything is computed
// with exact rational arithmetic; no floating-point approximation is used
// for any decision.
func Analyze(p Poly, a, b *big.Rat) (*Report, error) {
	if p.IsZero() {
		return nil, errors.New("zero polynomial rejected: every point would be a root")
	}
	if a.Cmp(b) >= 0 {
		return nil, fmt.Errorf("require a < b, got %s >= %s", a, b)
	}
	sf := p.SquareFree()
	rep := &Report{
		Original:   p,
		SquareFree: sf,
		A:          new(big.Rat).Set(a),
		B:          new(big.Rat).Set(b),
		RootAtA:    sf.Eval(a).Sign() == 0,
		RootAtB:    sf.Eval(b).Sign() == 0,
	}
	for _, iso := range isolateRoots(sf, a, b) {
		ev := Evidence{RootIsolation: iso}
		if iso.Exact == nil {
			ev.LowerVal = sf.Eval(iso.Lower)
			ev.UpperVal = sf.Eval(iso.Upper)
			if ev.LowerVal.Sign() == 0 || ev.UpperVal.Sign() == 0 ||
				ev.LowerVal.Sign() == ev.UpperVal.Sign() {
				return nil, fmt.Errorf("internal error: no sign change on (%s, %s)", iso.Lower, iso.Upper)
			}
		}
		rep.Roots = append(rep.Roots, ev)
	}
	rep.Count = len(rep.Roots)
	return rep, nil
}
