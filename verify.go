package ratbez

import (
	"errors"
	"fmt"
	"math/big"
)

// Verify independently checks a certificate produced by Analyze. It performs
// no search: every claim is verified by deterministic exact computations
// (Sturm root counts, exact sign evaluations, rational interval arithmetic,
// polynomial gcds and the value polynomial). It checks:
//
//  1. the derivative numerator M and its square-free part match the inputs;
//  2. the denominator has no root in [0,1] and is positive there;
//  3. the isolating intervals are ordered, disjoint, lie strictly inside
//     (0,1), each contains exactly one root of M, and no root is missing
//     (Sturm count over (0,1) equals the number of intervals);
//  4. the monotonicity sign of every segment is correct;
//  5. every candidate's value enclosure is reliable (interval evaluation of
//     N/D over the certified parameter interval stays inside it);
//  6. candidates in one group have exactly equal values (exact rational
//     comparison, polynomial gcd test, or shared value-polynomial root);
//  7. candidates in different groups have disjoint value enclosures, the max
//     (min) group dominates (is dominated by) every other group;
//  8. the global enclosures contain a member enclosure and have width <= eps.
func Verify(c *Cert) error {
	if c == nil {
		return errors.New("nil certificate")
	}
	if len(c.P) < 2 || len(c.P) > 6 {
		return fmt.Errorf("bad number of control values: %d", len(c.P))
	}
	if len(c.W) != len(c.P) {
		return errors.New("weights/control values length mismatch")
	}
	for i, w := range c.W {
		if w == nil || w.Sign() <= 0 {
			return fmt.Errorf("weight #%d is not positive", i)
		}
	}
	if c.Eps == nil || c.Eps.Sign() <= 0 {
		return errors.New("eps is not a positive rational")
	}

	N, D := buildND(c.P, c.W)
	g := GCD(N, D)
	N1, _ := QuoRem(N, g)
	D1, _ := QuoRem(D, g)
	M := Sub(Mul(N1.Deriv(), D1), Mul(N1, D1.Deriv()))

	if c.Constant {
		if !M.IsZero() {
			return errors.New("cert claims a constant curve but the derivative numerator is nonzero")
		}
		if c.ConstValue == nil {
			return errors.New("missing constant value")
		}
		if !Sub(N1, Scale(D1, c.ConstValue)).IsZero() {
			return errors.New("constant value does not satisfy N - c*D == 0")
		}
		return nil
	}
	if M.IsZero() {
		return errors.New("the curve is constant but the cert says otherwise")
	}

	Msf := SquareFree(M)
	if !equalStrings(intsToStrings(M.ToPrimitive()), c.MPrim) {
		return errors.New("derivative numerator M does not match the inputs")
	}
	if !equalStrings(polyToStrings(Msf), c.Msf) {
		return errors.New("square-free part of M does not match")
	}

	// Denominator strictly positive on [0,1]: no root inside and positive at 0.
	if D.Eval(ratZero).Sign() <= 0 || D.Eval(ratOne).Sign() <= 0 {
		return errors.New("denominator is not positive at the endpoints")
	}
	Dsf := SquareFree(D)
	if NewSturm(Dsf).CountOpen(ratZero, ratOne) != 0 {
		return errors.New("denominator has a root in (0,1)")
	}

	stM := NewSturm(Msf)
	// Root isolating intervals: ordered, disjoint, strictly inside (0,1),
	// each containing exactly one root.
	for i, r := range c.Roots {
		if r.Lo == nil || r.Hi == nil || r.Lo.Cmp(r.Hi) > 0 {
			return fmt.Errorf("root #%d: malformed interval", i)
		}
		if r.Lo.Sign() <= 0 || r.Hi.Cmp(ratOne) >= 0 {
			return fmt.Errorf("root #%d: interval not strictly inside (0,1)", i)
		}
		if i > 0 && c.Roots[i-1].Hi.Cmp(r.Lo) >= 0 {
			return fmt.Errorf("root #%d: intervals not disjoint and ordered", i)
		}
		if r.Lo.Cmp(r.Hi) == 0 {
			if Msf.Eval(r.Lo).Sign() != 0 {
				return fmt.Errorf("root #%d: claimed rational point is not a root", i)
			}
		} else {
			if Msf.Eval(r.Lo).Sign() == 0 || Msf.Eval(r.Hi).Sign() == 0 {
				return fmt.Errorf("root #%d: interval endpoint is a root", i)
			}
			if stM.CountOpen(r.Lo, r.Hi) != 1 {
				return fmt.Errorf("root #%d: interval does not contain exactly one root", i)
			}
		}
	}
	if got := stM.CountOpen(ratZero, ratOne); got != len(c.Roots) {
		return fmt.Errorf("incomplete root isolation: Sturm count in (0,1) is %d, cert has %d", got, len(c.Roots))
	}

	// Monotonicity segments.
	if len(c.Segments) != len(c.Roots)+1 {
		return fmt.Errorf("expected %d segments, got %d", len(c.Roots)+1, len(c.Segments))
	}
	bounds := []*big.Rat{ratZero}
	for _, r := range c.Roots {
		bounds = append(bounds, r.Lo, r.Hi)
	}
	bounds = append(bounds, ratOne)
	for i, seg := range c.Segments {
		lo, hi := bounds[2*i], bounds[2*i+1]
		if seg.Sample == nil || seg.Sample.Cmp(lo) <= 0 || seg.Sample.Cmp(hi) >= 0 {
			return fmt.Errorf("segment #%d: sample point not strictly inside the segment", i)
		}
		s := M.Eval(seg.Sample).Sign()
		if s == 0 || s != seg.Sign {
			return fmt.Errorf("segment #%d: monotonicity sign is wrong", i)
		}
	}

	// Value polynomial (required iff >= 2 interior stationary points).
	var Ssf Poly
	if len(c.Roots) >= 2 {
		S := valuePoly(Msf, N1, D1)
		if !equalStrings(polyToStrings(S), c.S) {
			return errors.New("value polynomial S does not match")
		}
		Ssf = SquareFree(S)
	}

	// Candidates.
	if len(c.Candidates) != len(c.Roots)+2 {
		return fmt.Errorf("expected %d candidates, got %d", len(c.Roots)+2, len(c.Candidates))
	}
	usedRoot := make([]bool, len(c.Roots))
	for i, cd := range c.Candidates {
		if cd.VLo == nil || cd.VHi == nil || cd.VLo.Cmp(cd.VHi) > 0 {
			return fmt.Errorf("candidate #%d: malformed value enclosure", i)
		}
		switch cd.Kind {
		case KindT0, KindT1:
			t := ratZero
			if cd.Kind == KindT1 {
				t = ratOne
			}
			v := new(big.Rat).Quo(N1.Eval(t), D1.Eval(t))
			if cd.VLo.Cmp(v) > 0 || cd.VHi.Cmp(v) < 0 {
				return fmt.Errorf("candidate #%d: endpoint value not enclosed", i)
			}
		case KindRoot:
			if cd.RootIndex < 0 || cd.RootIndex >= len(c.Roots) {
				return fmt.Errorf("candidate #%d: bad root index", i)
			}
			if usedRoot[cd.RootIndex] {
				return fmt.Errorf("candidate #%d: root #%d used twice", i, cd.RootIndex)
			}
			usedRoot[cd.RootIndex] = true
			r := c.Roots[cd.RootIndex]
			if cd.TL == nil || cd.TH == nil || cd.TL.Cmp(cd.TH) > 0 ||
				cd.TL.Cmp(r.Lo) < 0 || cd.TH.Cmp(r.Hi) > 0 {
				return fmt.Errorf("candidate #%d: parameter interval not inside its root interval", i)
			}
			if cd.TL.Cmp(cd.TH) == 0 {
				if Msf.Eval(cd.TL).Sign() != 0 {
					return fmt.Errorf("candidate #%d: parameter point is not a root", i)
				}
			} else {
				if Msf.Eval(cd.TL).Sign() == 0 || Msf.Eval(cd.TH).Sign() == 0 {
					return fmt.Errorf("candidate #%d: parameter interval endpoint is a root", i)
				}
				if stM.CountOpen(cd.TL, cd.TH) != 1 {
					return fmt.Errorf("candidate #%d: parameter interval does not isolate a root", i)
				}
			}
			if cd.VExact != nil {
				// The value is claimed to be exactly the rational VExact:
				// this holds iff gcd(Msf, N1 - VExact*D1) vanishes at the
				// isolated stationary point.
				if cd.VLo.Cmp(cd.VExact) != 0 || cd.VHi.Cmp(cd.VExact) != 0 {
					return fmt.Errorf("candidate #%d: exact value inconsistent with enclosure", i)
				}
				gv := GCD(Msf, Sub(N1, Scale(D1, cd.VExact)))
				if !polyHasRootIn(gv, Iv{cd.TL, cd.TH}) {
					return fmt.Errorf("candidate #%d: exact value claim not certified", i)
				}
			} else {
				vIv, ok := valueEnclosure(N1, D1, Iv{cd.TL, cd.TH})
				if !ok {
					return fmt.Errorf("candidate #%d: denominator interval contains 0", i)
				}
				if vIv.Lo.Cmp(cd.VLo) < 0 || vIv.Hi.Cmp(cd.VHi) > 0 {
					return fmt.Errorf("candidate #%d: value enclosure is not reliable", i)
				}
			}
		default:
			return fmt.Errorf("candidate #%d: unknown kind %q", i, cd.Kind)
		}
	}
	for i, u := range usedRoot {
		if !u {
			return fmt.Errorf("root #%d has no candidate", i)
		}
	}

	// Group equality and cross-group inequality.
	for i := 0; i < len(c.Candidates); i++ {
		for j := i + 1; j < len(c.Candidates); j++ {
			ci, cj := c.Candidates[i], c.Candidates[j]
			if ci.Group == cj.Group {
				if err := verifyEqual(c, i, j, Msf, N1, D1, Ssf); err != nil {
					return err
				}
			} else if !(ci.VHi.Cmp(cj.VLo) < 0 || cj.VHi.Cmp(ci.VLo) < 0) {
				return fmt.Errorf("candidates #%d and #%d: different groups but value enclosures overlap", i, j)
			}
		}
	}

	// Max/min group dominance.
	hasMax, hasMin := false, false
	for i, ci := range c.Candidates {
		if ci.Group == c.MaxGroup {
			hasMax = true
		}
		if ci.Group == c.MinGroup {
			hasMin = true
		}
		for j, cj := range c.Candidates {
			if i == j || ci.Group == cj.Group {
				continue
			}
			if ci.Group == c.MaxGroup && cj.VHi.Cmp(ci.VLo) >= 0 {
				return fmt.Errorf("max group does not dominate candidate #%d", j)
			}
			if ci.Group == c.MinGroup && cj.VLo.Cmp(ci.VHi) <= 0 {
				return fmt.Errorf("min group is not dominated by candidate #%d", j)
			}
		}
	}
	if !hasMax || !hasMin {
		return errors.New("max/min group has no member")
	}
	if c.MaxGroup == c.MinGroup {
		return errors.New("max and min groups coincide on a non-constant curve")
	}

	// Global enclosures: width <= eps and containing a member enclosure.
	if c.MaxLo == nil || c.MaxHi == nil || c.MinLo == nil || c.MinHi == nil {
		return errors.New("missing global enclosures")
	}
	if new(big.Rat).Sub(c.MaxHi, c.MaxLo).Cmp(c.Eps) > 0 {
		return errors.New("max enclosure is wider than eps")
	}
	if new(big.Rat).Sub(c.MinHi, c.MinLo).Cmp(c.Eps) > 0 {
		return errors.New("min enclosure is wider than eps")
	}
	okMax, okMin := false, false
	for _, cd := range c.Candidates {
		if cd.Group == c.MaxGroup && cd.VLo.Cmp(c.MaxLo) >= 0 && cd.VHi.Cmp(c.MaxHi) <= 0 {
			okMax = true
		}
		if cd.Group == c.MinGroup && cd.VLo.Cmp(c.MinLo) >= 0 && cd.VHi.Cmp(c.MinHi) <= 0 {
			okMin = true
		}
	}
	if !okMax {
		return errors.New("max enclosure contains no member enclosure")
	}
	if !okMin {
		return errors.New("min enclosure contains no member enclosure")
	}
	return nil
}

// verifyEqual checks that two candidates of the same group have exactly equal values.
func verifyEqual(c *Cert, i, j int, Msf, N1, D1, Ssf Poly) error {
	ci, cj := c.Candidates[i], c.Candidates[j]
	endpointVal := func(cd CandCert) (*big.Rat, bool) {
		switch cd.Kind {
		case KindT0:
			return new(big.Rat).Quo(N1.Eval(ratZero), D1.Eval(ratZero)), true
		case KindT1:
			return new(big.Rat).Quo(N1.Eval(ratOne), D1.Eval(ratOne)), true
		}
		return nil, false
	}
	vi, ei := endpointVal(ci)
	vj, ej := endpointVal(cj)
	switch {
	case ei && ej:
		if vi.Cmp(vj) != 0 {
			return fmt.Errorf("candidates #%d and #%d: same group but endpoint values differ", i, j)
		}
		return nil
	case ei != ej:
		// endpoint vs interior root: v == R(alpha) iff gcd(Msf, N1 - v*D1)
		// vanishes at alpha.
		v, root := vi, cj
		if ej {
			v, root = vj, ci
		}
		gv := GCD(Msf, Sub(N1, Scale(D1, v)))
		if gv.Deg() < 1 {
			return fmt.Errorf("candidates #%d and #%d: claimed endpoint/interior tie does not hold", i, j)
		}
		if root.TL.Cmp(root.TH) == 0 {
			if gv.Eval(root.TL).Sign() != 0 {
				return fmt.Errorf("candidates #%d and #%d: claimed endpoint/interior tie does not hold", i, j)
			}
			return nil
		}
		if NewSturm(gv).CountOpen(root.TL, root.TH) != 1 {
			return fmt.Errorf("candidates #%d and #%d: claimed endpoint/interior tie does not hold", i, j)
		}
		return nil
	default:
		// interior vs interior: both value enclosures must lie in the same
		// isolating interval of the value polynomial, which then contains
		// their common value (the unique root of S there).
		if Ssf.IsZero() {
			return fmt.Errorf("candidates #%d and #%d: interior tie claimed but no value polynomial", i, j)
		}
		if ci.SL == nil || ci.SH == nil || cj.SL == nil || cj.SH == nil ||
			ci.SL.Cmp(cj.SL) != 0 || ci.SH.Cmp(cj.SH) != 0 {
			return fmt.Errorf("candidates #%d and #%d: interior tie without a shared value-polynomial root", i, j)
		}
		sIv := Iv{ci.SL, ci.SH}
		if ci.SL.Cmp(ci.SH) == 0 {
			if Ssf.Eval(ci.SL).Sign() != 0 {
				return fmt.Errorf("candidates #%d and #%d: value-polynomial point is not a root", i, j)
			}
		} else {
			if Ssf.Eval(ci.SL).Sign() == 0 || Ssf.Eval(ci.SH).Sign() == 0 {
				return fmt.Errorf("candidates #%d and #%d: value-polynomial interval endpoint is a root", i, j)
			}
			if NewSturm(Ssf).CountOpen(sIv.Lo, sIv.Hi) != 1 {
				return fmt.Errorf("candidates #%d and #%d: value-polynomial interval does not isolate a root", i, j)
			}
		}
		for k, cd := range []CandCert{ci, cj} {
			if cd.VLo.Cmp(sIv.Lo) < 0 || cd.VHi.Cmp(sIv.Hi) > 0 {
				return fmt.Errorf("candidate #%d (pair %d,%d): value enclosure not inside the value-polynomial interval", k, i, j)
			}
		}
		return nil
	}
}
