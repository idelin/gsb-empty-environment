package rational

import (
	"errors"
	"sort"
)

// RationalFunction is Num/Den. Den must be non-zero for the function to
// be meaningful; Simplify and Equivalent reject a zero denominator.
type RationalFunction struct {
	Num *Poly
	Den *Poly
}

// Pole is a real pole of the reduced function with its multiplicity.
type Pole struct {
	Root         IsolatedRoot
	Multiplicity int
}

// Simplified is the result of Simplify. Num/Den is the reduced form
// (Den monic, gcd(Num, Den) = 1). Crucially, the reduced form does NOT
// define the original function at the holes: Holes lists the removable
// discontinuities inherited from the original domain, Excluded lists
// every real point where the original denominator vanished (holes and
// pole locations), and Poles lists the surviving poles with their
// multiplicities in the reduced denominator.
type Simplified struct {
	Num      *Poly
	Den      *Poly
	Holes    []IsolatedRoot
	Poles    []Pole
	Excluded []IsolatedRoot
}

// Simplify reduces f to lowest terms while recording the domain of the
// original function. A real root r of the original denominator with
// multiplicity mDen, where the numerator vanishes at r with
// multiplicity mNum, is:
//   - a hole (removable discontinuity) when mNum >= mDen, or
//   - a pole of multiplicity mDen - mNum otherwise.
func Simplify(f RationalFunction) (*Simplified, error) {
	if f.Den == nil || f.Den.IsZero() {
		return nil, errors.New("rational: zero denominator")
	}
	num := f.Num
	if num == nil {
		num = Zero()
	}
	g := GCD(num, f.Den)
	rNum, _ := DivMod(num, g)
	rDen, _ := DivMod(f.Den, g)
	// Normalize: reduced denominator monic.
	lc := rDen.LeadingCoeff()
	if lc.Sign() != 0 {
		inv := lc.Inv(lc)
		rNum = rNum.Scale(inv)
		rDen = rDen.Scale(inv)
	}
	s := &Simplified{Num: rNum, Den: rDen}
	for _, sf := range f.Den.SquareFreeFactorization() {
		for _, root := range isolateRealRoots(sf.Factor) {
			root := root
			mDen := sf.Mult
			mNum := 0
			hole := num.IsZero()
			if !hole {
				mNum = multiplicityAtRoot(num, sf.Factor, &root)
				hole = mNum >= mDen
			}
			if hole {
				s.Holes = append(s.Holes, root)
			} else {
				s.Poles = append(s.Poles, Pole{Root: root, Multiplicity: mDen - mNum})
			}
			s.Excluded = append(s.Excluded, root)
		}
	}
	sortRoots(s.Holes)
	sortRoots(s.Excluded)
	sort.Slice(s.Poles, func(i, j int) bool {
		return s.Poles[i].Root.Lo.Cmp(s.Poles[j].Root.Lo) < 0
	})
	return s, nil
}

// multiplicityAtRoot returns the multiplicity with which the isolated
// root of the square-free polynomial f occurs in num (0 if num does not
// vanish there). It repeatedly strips gcd(num, f), which is square-free
// and contains exactly the roots of f shared with num, one copy at a
// time.
func multiplicityAtRoot(num, f *Poly, root *IsolatedRoot) int {
	m := 0
	for !num.IsZero() {
		h := GCD(num, f)
		if h.IsConstant() {
			break
		}
		// h is square-free; its roots are a subset of the roots of
		// f, so the isolating endpoints are never roots of h.
		if sturmCount(sturmSequence(h), root.Lo, root.Hi) != 1 {
			break
		}
		num, _ = DivMod(num, h)
		m++
	}
	return m
}

// EquivMode selects the notion of domain used by Equivalent.
type EquivMode int

const (
	// SameDomain requires the two functions to have identical domains:
	// the real roots of the two denominators must coincide as sets, in
	// addition to algebraic equality.
	SameDomain EquivMode = iota
	// CommonDomain only requires the functions to agree on the
	// intersection of their domains, i.e. algebraic equality of the
	// rational expressions. Holes may differ.
	CommonDomain
)

// Equivalent reports whether f and g are equal under the given mode.
// In both modes the rational expressions must be algebraically equal
// (f.Num*g.Den == g.Num*f.Den); SameDomain additionally requires equal
// excluded-point sets.
func Equivalent(f, g RationalFunction, mode EquivMode) bool {
	if f.Den == nil || f.Den.IsZero() || g.Den == nil || g.Den.IsZero() {
		return false
	}
	fn, gn := f.Num, g.Num
	if fn == nil {
		fn = Zero()
	}
	if gn == nil {
		gn = Zero()
	}
	if !Mul(fn, g.Den).Equal(Mul(gn, f.Den)) {
		return false
	}
	if mode == CommonDomain {
		return true
	}
	return sameRealRootSet(f.Den, g.Den)
}
