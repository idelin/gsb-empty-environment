// Package plsolver minimizes one-dimensional piecewise-linear objectives
//
//	f(x) = sum_i w_i*|x - a_i|  +  sum_j c_j*x + d_j,     x in [Lo, Hi]
//
// with exact rational arithmetic. Weights may be negative, so f is in
// general non-convex and median-style arguments do not apply. Solve
// computes the exact global minimum and the full set of minimizers as a
// sorted union of closed intervals, together with per-segment slope
// certificates. Terms can be added and removed incrementally.
package plsolver

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type termKind int

const (
	absTerm termKind = iota // w*|x - a|
	linTerm                 // w*x + c
)

type term struct {
	kind termKind
	w    *big.Rat // weight of |x-a|, or coefficient of x
	a    *big.Rat // breakpoint (abs terms only)
	c    *big.Rat // additive constant (linear terms only)
}

// Solver holds the domain and the current multiset of objective terms.
type Solver struct {
	lo, hi *big.Rat
	terms  map[int]term
	next   int
}

// NewSolver builds a solver over the closed domain [lo, hi].
func NewSolver(lo, hi *big.Rat) *Solver {
	if lo.Cmp(hi) > 0 {
		panic("plsolver: empty domain")
	}
	return &Solver{
		lo:    new(big.Rat).Set(lo),
		hi:    new(big.Rat).Set(hi),
		terms: make(map[int]term),
	}
}

// AddAbs adds the term w*|x - a| and returns its id. w may be negative.
func (s *Solver) AddAbs(w, a *big.Rat) int {
	id := s.next
	s.next++
	s.terms[id] = term{kind: absTerm, w: new(big.Rat).Set(w), a: new(big.Rat).Set(a)}
	return id
}

// AddLinear adds the term coef*x + constant and returns its id.
func (s *Solver) AddLinear(coef, constant *big.Rat) int {
	id := s.next
	s.next++
	s.terms[id] = term{kind: linTerm, w: new(big.Rat).Set(coef), c: new(big.Rat).Set(constant)}
	return id
}

// Remove deletes the term with the given id; it reports whether it existed.
func (s *Solver) Remove(id int) bool {
	if _, ok := s.terms[id]; !ok {
		return false
	}
	delete(s.terms, id)
	return true
}

// Eval evaluates the objective exactly at x.
func (s *Solver) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	tmp := new(big.Rat)
	for _, t := range s.terms {
		switch t.kind {
		case absTerm:
			tmp.Sub(x, t.a).Abs(tmp)
			tmp.Mul(tmp, t.w)
			acc.Add(acc, tmp)
		case linTerm:
			tmp.Mul(t.w, x)
			acc.Add(acc, tmp)
			acc.Add(acc, t.c)
		}
	}
	return acc
}

// slopeAt returns the exact slope of f at x. x must not coincide with a
// breakpoint of an absolute-value term.
func (s *Solver) slopeAt(x *big.Rat) *big.Rat {
	slope := new(big.Rat)
	tmp := new(big.Rat)
	for _, t := range s.terms {
		switch t.kind {
		case absTerm:
			switch tmp.Sub(x, t.a).Sign() {
			case 1:
				slope.Add(slope, t.w)
			case -1:
				slope.Sub(slope, t.w)
			}
		case linTerm:
			slope.Add(slope, t.w)
		}
	}
	return slope
}

// breakpoints returns the sorted, duplicate-merged list of domain
// endpoints and interior breakpoints.
func (s *Solver) breakpoints() []*big.Rat {
	pts := []*big.Rat{new(big.Rat).Set(s.lo), new(big.Rat).Set(s.hi)}
	for _, t := range s.terms {
		if t.kind != absTerm {
			continue
		}
		if t.a.Cmp(s.lo) > 0 && t.a.Cmp(s.hi) < 0 {
			pts = append(pts, new(big.Rat).Set(t.a))
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Cmp(pts[j]) < 0 })
	out := pts[:1]
	for _, p := range pts[1:] {
		if p.Cmp(out[len(out)-1]) != 0 {
			out = append(out, p)
		}
	}
	return out
}

// Interval is a closed optimal interval [Lo, Hi]; Lo == Hi for an
// isolated optimal point.
type Interval struct{ Lo, Hi *big.Rat }

func (iv Interval) String() string {
	if iv.Lo.Cmp(iv.Hi) == 0 {
		return fmt.Sprintf("{%s}", iv.Lo)
	}
	return fmt.Sprintf("[%s, %s]", iv.Lo, iv.Hi)
}

// PointInfo records the exact objective value at a merged breakpoint.
type PointInfo struct {
	X, Value *big.Rat
	Optimal  bool
}

// SegmentInfo is the slope certificate for one open elementary segment
// (Lo, Hi) between consecutive merged breakpoints.
type SegmentInfo struct {
	Lo, Hi *big.Rat
	Slope  *big.Rat
}

// Trend classifies the segment: f strictly decreases, strictly
// increases, or is constant on it.
func (s SegmentInfo) Trend() string {
	switch s.Slope.Sign() {
	case 1:
		return "increasing"
	case -1:
		return "decreasing"
	default:
		return "flat"
	}
}

// Result is the exact solution of one Solve call.
type Result struct {
	Min       *big.Rat      // global minimum value
	Intervals []Interval    // all minimizers, sorted and disjoint
	Points    []PointInfo   // value at every merged breakpoint
	Segments  []SegmentInfo // slope certificate per elementary segment
}

// Solve computes the exact global minimum and the full optimal set.
//
// f is linear on every elementary segment, so its minimum over the
// domain is attained at a breakpoint; a whole segment is optimal iff it
// is flat and its (equal) endpoint values attain the minimum.
func (s *Solver) Solve() *Result {
	pts := s.breakpoints()
	n := len(pts)
	res := &Result{}

	vals := make([]*big.Rat, n)
	for i, p := range pts {
		vals[i] = s.Eval(p)
	}
	for k := 0; k+1 < n; k++ {
		mid := new(big.Rat).Add(pts[k], pts[k+1])
		mid.Quo(mid, big.NewRat(2, 1))
		res.Segments = append(res.Segments, SegmentInfo{
			Lo:    pts[k],
			Hi:    pts[k+1],
			Slope: s.slopeAt(mid),
		})
	}

	res.Min = new(big.Rat).Set(vals[0])
	for _, v := range vals[1:] {
		if v.Cmp(res.Min) < 0 {
			res.Min.Set(v)
		}
	}

	optPoint := make([]bool, n)
	for i := range pts {
		optPoint[i] = vals[i].Cmp(res.Min) == 0
		res.Points = append(res.Points, PointInfo{X: pts[i], Value: vals[i], Optimal: optPoint[i]})
	}
	optSeg := make([]bool, n-1)
	for k := range optSeg {
		optSeg[k] = res.Segments[k].Slope.Sign() == 0 && optPoint[k]
	}

	// Merge adjacent optimal atoms (points and flat segments) into
	// maximal closed intervals.
	for i := 0; i < n; {
		if !optPoint[i] {
			i++
			continue
		}
		j := i
		for j < n-1 && optSeg[j] {
			j++
		}
		res.Intervals = append(res.Intervals, Interval{Lo: pts[i], Hi: pts[j]})
		i = j + 1
	}
	return res
}

// Evidence renders a human-readable certificate: the minimum, the
// optimal intervals, the value at every breakpoint, and the slope of
// every elementary segment.
func (r *Result) Evidence() string {
	var b strings.Builder
	fmt.Fprintf(&b, "global minimum: %s\n", r.Min)
	for _, iv := range r.Intervals {
		fmt.Fprintf(&b, "optimal set component: %s\n", iv)
	}
	for _, p := range r.Points {
		mark := ""
		if p.Optimal {
			mark = "  <-- optimal"
		}
		fmt.Fprintf(&b, "breakpoint x=%-6s f(x)=%-8s%s\n", p.X, p.Value, mark)
	}
	for _, seg := range r.Segments {
		fmt.Fprintf(&b, "segment (%s, %s): slope = %s (%s)\n",
			seg.Lo, seg.Hi, seg.Slope, seg.Trend())
	}
	return b.String()
}
