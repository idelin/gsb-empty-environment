package main

import (
	"fmt"
	"math/big"
	"strings"
)

// ratFunc is a rational function num/den with den != 0.
type ratFunc struct {
	num poly
	den poly
}

// piecewise is a piecewise rational function on [breaks[0], breaks[last]].
// Piece i covers [breaks[i], breaks[i+1]); the last piece also includes the
// right endpoint. Values at breakpoints therefore follow the
// right-continuous convention, and the domain's right end is included
// separately.
type piecewise struct {
	breaks []*big.Rat
	pieces []ratFunc
}

// pieceAt returns the index of the piece active at x (right-continuous).
func (pw *piecewise) pieceAt(x *big.Rat) int {
	j := 0
	for k := 1; k < len(pw.breaks); k++ {
		if pw.breaks[k].Cmp(x) <= 0 {
			j = k
		} else {
			break
		}
	}
	if j >= len(pw.pieces) {
		j = len(pw.pieces) - 1
	}
	return j
}

func mergeBreaks(a, b []*big.Rat) []*big.Rat {
	var out []*big.Rat
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var next *big.Rat
		switch {
		case i >= len(a):
			next = b[j]
			j++
		case j >= len(b):
			next = a[i]
			i++
		default:
			switch a[i].Cmp(b[j]) {
			case 0:
				next = a[i]
				i++
				j++
			case -1:
				next = a[i]
				i++
			default:
				next = b[j]
				j++
			}
		}
		if len(out) == 0 || out[len(out)-1].Cmp(next) != 0 {
			out = append(out, next)
		}
	}
	return out
}

// reducedForm returns num/den with common factors cancelled and a monic
// denominator, as evidence that two pieces are the same rational function.
func reducedForm(rf ratFunc) string {
	if rf.num.isZero() {
		return "0 / 1"
	}
	g := gcdPoly(rf.num, rf.den)
	n := quoExact(rf.num, g)
	d := quoExact(rf.den, g)
	l := d.lead()
	if l.Cmp(big.NewRat(1, 1)) != 0 {
		n = scalePoly(n, new(big.Rat).Quo(big.NewRat(1, 1), l))
		d = monic(d)
	}
	if d.deg() == 0 {
		return n.String()
	}
	return fmt.Sprintf("(%s) / (%s)", n.String(), d.String())
}

type pointRes struct {
	rt  root
	seg string
}

type rejected struct {
	rt  root
	seg string
}

type overlap struct {
	lo, hi      *big.Rat
	rightClosed bool
	forms       []string
	excluded    []root
}

type report struct {
	breaks   []*big.Rat
	points   []pointRes
	overlaps []overlap
	rejected []rejected
}

func segLabel(l, r *big.Rat, closedRight bool) string {
	if closedRight {
		return fmt.Sprintf("[%s, %s]", ratStr(l), ratStr(r))
	}
	return fmt.Sprintf("[%s, %s)", ratStr(l), ratStr(r))
}

// analyze finds all equality points and coincident intervals of f and g on
// their common domain.
func analyze(f, g *piecewise, eps *big.Rat) (*report, error) {
	if f.breaks[0].Cmp(g.breaks[0]) != 0 ||
		f.breaks[len(f.breaks)-1].Cmp(g.breaks[len(g.breaks)-1]) != 0 {
		return nil, fmt.Errorf("f and g must share the same domain endpoints")
	}
	breaks := mergeBreaks(f.breaks, g.breaks)
	rep := &report{breaks: breaks}
	for i := 0; i+1 < len(breaks); i++ {
		l, r := breaks[i], breaks[i+1]
		last := i+2 == len(breaks)
		fp := f.pieces[f.pieceAt(l)]
		gp := g.pieces[g.pieceAt(l)]
		// f - g = (Nf*Dg - Ng*Df) / (Df*Dg)
		dn := subPoly(mulPoly(fp.num, gp.den), mulPoly(gp.num, fp.den))
		dd := mulPoly(fp.den, gp.den)
		seg := segLabel(l, r, last)
		if dn.isZero() {
			// Identically zero difference: f == g as rational functions on
			// this whole sub-interval. Report a coincident interval instead
			// of isolating "infinitely many roots".
			rep.addOverlap(l, r, last, reducedForm(fp))
			ex := findRoots(dd, l, r, last, eps)
			ov := &rep.overlaps[len(rep.overlaps)-1]
			ov.excluded = append(ov.excluded, ex...)
			continue
		}
		cands := findRoots(dn, l, r, last, eps)
		if len(cands) == 0 {
			continue
		}
		assignMult(dn, cands)
		var h poly
		var hst *sturm
		for _, c := range cands {
			// A root of the difference numerator only counts if f and g
			// are both defined there (denominators non-zero).
			undefined := false
			if c.exact != nil {
				undefined = evalPoly(dd, c.exact).Sign() == 0
			} else {
				if h == nil {
					h = gcdPoly(dn, dd)
					hst = newSturm(h)
				}
				undefined = hst.countOpen(c.lo, c.hi) == 1
			}
			if undefined {
				rep.rejected = append(rep.rejected, rejected{rt: c, seg: seg})
			} else {
				rep.points = append(rep.points, pointRes{rt: c, seg: seg})
			}
		}
	}
	return rep, nil
}

func (rep *report) addOverlap(l, r *big.Rat, closedRight bool, form string) {
	n := len(rep.overlaps)
	if n > 0 && rep.overlaps[n-1].hi.Cmp(l) == 0 {
		rep.overlaps[n-1].hi = r
		rep.overlaps[n-1].rightClosed = closedRight
		found := false
		for _, s := range rep.overlaps[n-1].forms {
			if s == form {
				found = true
			}
		}
		if !found {
			rep.overlaps[n-1].forms = append(rep.overlaps[n-1].forms, form)
		}
		return
	}
	rep.overlaps = append(rep.overlaps, overlap{
		lo: l, hi: r, rightClosed: closedRight, forms: []string{form},
	})
}

func multDesc(m int) string {
	switch {
	case m <= 1:
		return "mult 1 (transversal crossing)"
	case m%2 == 0:
		return fmt.Sprintf("mult %d (tangency: curves touch, counts as equal)", m)
	default:
		return fmt.Sprintf("mult %d (odd multiplicity, crossing)", m)
	}
}

func (rep *report) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Domain: [%s, %s]  (breakpoint values right-continuous; right endpoint included separately)\n",
		ratStr(rep.breaks[0]), ratStr(rep.breaks[len(rep.breaks)-1]))
	parts := make([]string, len(rep.breaks))
	for i, b := range rep.breaks {
		parts[i] = ratStr(b)
	}
	fmt.Fprintf(&sb, "Refined breakpoints (union of f and g): %s\n\n", strings.Join(parts, ", "))

	sb.WriteString("== Coincident intervals (f and g identically equal) ==\n")
	if len(rep.overlaps) == 0 {
		sb.WriteString("  (none)\n")
	}
	for _, ov := range rep.overlaps {
		fmt.Fprintf(&sb, "  %s\n", segLabel(ov.lo, ov.hi, ov.rightClosed))
		sb.WriteString("    evidence: Nf*Dg - Ng*Df is the zero polynomial on every constituent piece\n")
		sb.WriteString("    (identically-zero difference => whole-interval coincidence, not infinitely many roots)\n")
		for _, form := range ov.forms {
			fmt.Fprintf(&sb, "    reduced common form: %s\n", form)
		}
		for _, ex := range ov.excluded {
			fmt.Fprintf(&sb, "    excluded point (f,g undefined, denominator zero): x = %s\n", ex.String())
		}
	}

	sb.WriteString("\n== Equality points (isolated roots of the difference) ==\n")
	if len(rep.points) == 0 {
		sb.WriteString("  (none)\n")
	}
	for _, p := range rep.points {
		fmt.Fprintf(&sb, "  x = %-26s %-46s piece %s\n", p.rt.String(), multDesc(p.rt.mult), p.seg)
	}

	sb.WriteString("\n== Rejected candidates (difference numerator zero, but f,g undefined there) ==\n")
	if len(rep.rejected) == 0 {
		sb.WriteString("  (none)\n")
	}
	for _, rj := range rep.rejected {
		fmt.Fprintf(&sb, "  x = %-26s denominator vanishes        piece %s\n", rj.rt.String(), rj.seg)
	}

	fmt.Fprintf(&sb, "\nSummary: %d equality point(s), %d coincident interval(s), %d rejected candidate(s)\n",
		len(rep.points), len(rep.overlaps), len(rep.rejected))
	return sb.String()
}
