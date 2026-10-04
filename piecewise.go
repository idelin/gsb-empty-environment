package piecewise

import (
	"math/big"
	"sort"
	"strings"
)

// piece is one polynomial segment defined on the half-open interval
// [lo, hi). Invariant: lo < hi. Pieces are immutable and shared between
// versions of a Piecewise.
type piece struct {
	lo, hi *big.Rat
	poly   Poly
}

// Piecewise is an immutable piecewise-polynomial function over the
// rationals. Each piece owns the half-open interval [lo, hi), so at a
// breakpoint the right-hand piece defines the value while the left-hand
// piece defines the left limit; the two may differ (jump discontinuity).
//
// The zero value is the empty function (defined nowhere). All operations
// return new versions; the receiver is never mutated.
type Piecewise struct {
	pieces []piece // sorted by lo, non-overlapping
}

// New returns an empty piecewise function.
func New() *Piecewise { return &Piecewise{} }

// NumPieces returns the number of polynomial segments.
func (pw *Piecewise) NumPieces() int { return len(pw.pieces) }

// Piece returns the bounds and polynomial of the i-th segment
// (in increasing order). Bounds are copies; the polynomial is immutable.
func (pw *Piecewise) Piece(i int) (lo, hi *big.Rat, p Poly) {
	pc := pw.pieces[i]
	return new(big.Rat).Set(pc.lo), new(big.Rat).Set(pc.hi), pc.poly
}

// Replace returns a new version in which the function equals p on
// [lo, hi). Existing material overlapping [lo, hi) is removed (and the
// affected pieces are clipped, never mutated), so the receiver and every
// previously returned version stay valid.
//
// A degenerate interval (lo >= hi) is a no-op and returns the receiver.
// Adjacent pieces with identical polynomials are merged.
func (pw *Piecewise) Replace(lo, hi *big.Rat, p Poly) *Piecewise {
	if lo.Cmp(hi) >= 0 {
		return pw
	}
	out := make([]piece, 0, len(pw.pieces)+1)
	for _, pc := range pw.pieces {
		if pc.hi.Cmp(lo) <= 0 || pc.lo.Cmp(hi) >= 0 {
			out = append(out, pc) // disjoint: keep as is
			continue
		}
		if pc.lo.Cmp(lo) < 0 {
			out = append(out, piece{pc.lo, lo, pc.poly})
		}
		if pc.hi.Cmp(hi) > 0 {
			out = append(out, piece{hi, pc.hi, pc.poly})
		}
	}
	out = append(out, piece{lo, hi, p})
	sort.Slice(out, func(i, j int) bool { return out[i].lo.Cmp(out[j].lo) < 0 })
	return &Piecewise{pieces: merge(out)}
}

// merge fuses adjacent pieces that share a breakpoint and carry the same
// polynomial. Remaining shared breakpoints are genuine coincident
// breakpoints with (potentially) different left/right behaviour.
func merge(in []piece) []piece {
	out := in[:0]
	for _, pc := range in {
		if n := len(out); n > 0 &&
			out[n-1].hi.Cmp(pc.lo) == 0 &&
			out[n-1].poly.Equal(pc.poly) {
			out[n-1].hi = pc.hi
			continue
		}
		out = append(out, pc)
	}
	return out
}

// rightPiece returns the piece determining the right limit at x:
// the unique piece with lo <= x < hi, or nil.
func (pw *Piecewise) rightPiece(x *big.Rat) *piece {
	for i := range pw.pieces {
		if pw.pieces[i].lo.Cmp(x) <= 0 && x.Cmp(pw.pieces[i].hi) < 0 {
			return &pw.pieces[i]
		}
	}
	return nil
}

// leftPiece returns the piece determining the left limit at x:
// the unique piece with lo < x <= hi, or nil.
func (pw *Piecewise) leftPiece(x *big.Rat) *piece {
	for i := range pw.pieces {
		if pw.pieces[i].lo.Cmp(x) < 0 && x.Cmp(pw.pieces[i].hi) <= 0 {
			return &pw.pieces[i]
		}
	}
	return nil
}

// Eval returns the (right-continuous) value at x. ok is false when x is
// outside the domain.
func (pw *Piecewise) Eval(x *big.Rat) (val *big.Rat, ok bool) {
	return pw.EvalRight(x)
}

// EvalLeft returns the left limit at x (the left-hand piece evaluated at
// x). ok is false when no piece reaches x from the left.
func (pw *Piecewise) EvalLeft(x *big.Rat) (val *big.Rat, ok bool) {
	if pc := pw.leftPiece(x); pc != nil {
		return pc.poly.Eval(x), true
	}
	return nil, false
}

// EvalRight returns the right limit at x (the right-hand piece evaluated
// at x). ok is false when no piece starts at or covers x.
func (pw *Piecewise) EvalRight(x *big.Rat) (val *big.Rat, ok bool) {
	if pc := pw.rightPiece(x); pc != nil {
		return pc.poly.Eval(x), true
	}
	return nil, false
}

// Deriv returns the derivative at x. ok is false when x is outside the
// domain or lies on a breakpoint (including coincident breakpoints and
// jump discontinuities); use DerivLeft/DerivRight there.
func (pw *Piecewise) Deriv(x *big.Rat) (val *big.Rat, ok bool) {
	for i := range pw.pieces {
		pc := &pw.pieces[i]
		if pc.lo.Cmp(x) < 0 && x.Cmp(pc.hi) < 0 {
			return pc.poly.Deriv().Eval(x), true
		}
	}
	return nil, false
}

// DerivLeft returns the derivative of the left-hand piece at x.
func (pw *Piecewise) DerivLeft(x *big.Rat) (val *big.Rat, ok bool) {
	if pc := pw.leftPiece(x); pc != nil {
		return pc.poly.Deriv().Eval(x), true
	}
	return nil, false
}

// DerivRight returns the derivative of the right-hand piece at x.
func (pw *Piecewise) DerivRight(x *big.Rat) (val *big.Rat, ok bool) {
	if pc := pw.rightPiece(x); pc != nil {
		return pc.poly.Deriv().Eval(x), true
	}
	return nil, false
}

// Integral returns the exact integral over [a, b], computed as the sum of
// independent per-segment integrals. Single endpoints have measure zero
// and are never double counted, even at breakpoints. Bounds outside the
// domain are clipped to the defined pieces (see Covers).
//
// Reversed bounds negate the result; equal bounds give 0.
func (pw *Piecewise) Integral(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) > 0 {
		return new(big.Rat).Neg(pw.Integral(b, a))
	}
	total := new(big.Rat)
	for i := range pw.pieces {
		pc := &pw.pieces[i]
		lo := maxRat(pc.lo, a)
		hi := minRat(pc.hi, b)
		if lo.Cmp(hi) < 0 {
			total.Add(total, pc.poly.Integral(lo, hi))
		}
	}
	return total
}

// Covers reports whether the whole interval [a, b] lies inside the
// domain. Empty intervals are trivially covered.
func (pw *Piecewise) Covers(a, b *big.Rat) bool {
	if a.Cmp(b) >= 0 {
		return true
	}
	cur := a
	for i := range pw.pieces {
		pc := &pw.pieces[i]
		if pc.hi.Cmp(cur) <= 0 {
			continue
		}
		if pc.lo.Cmp(cur) > 0 {
			return false
		}
		cur = pc.hi
		if cur.Cmp(b) >= 0 {
			return true
		}
	}
	return false
}

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func (pw *Piecewise) String() string {
	if len(pw.pieces) == 0 {
		return "<empty>"
	}
	parts := make([]string, len(pw.pieces))
	for i, pc := range pw.pieces {
		parts[i] = "[" + pc.lo.RatString() + ", " + pc.hi.RatString() + "): " + pc.poly.String()
	}
	return strings.Join(parts, "  ")
}
