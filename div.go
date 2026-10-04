package affine

import (
	"errors"
	"math"
)

// ErrZeroDivisor is returned when the divisor's range contains zero.
// Division is explicitly rejected rather than silently producing
// unbounded or disconnected results; callers that need the piecewise
// domain can inspect the divisor's Range and split it at zero.
var ErrZeroDivisor = errors.New("affine: divisor range contains zero")

// SplitDomain returns the connected sub-domains of [lo, hi] on which
// 1/x is defined, i.e. the pieces separated by zero. It returns one or
// two non-empty open-ended intervals; a zero-width piece is dropped.
func SplitDomain(lo, hi float64) [][2]float64 {
	var out [][2]float64
	if hi < 0 || lo > 0 {
		return append(out, [2]float64{lo, hi})
	}
	if lo < 0 {
		out = append(out, [2]float64{lo, 0})
	}
	if hi > 0 {
		out = append(out, [2]float64{0, hi})
	}
	return out
}

// Recip returns a conservative affine enclosure of 1/f. It fails with
// ErrZeroDivisor when f's range contains zero.
//
// The Chebyshev-style linear approximation of 1/x on [lo,hi] (same
// sign) uses slope alpha = -1/(lo*hi); the residual between 1/x and
// alpha*x + zeta over the interval is enclosed by a remainder symbol
// of magnitude delta.
func (f Form) Recip() (Form, error) {
	lo, hi := f.Range()
	if lo <= 0 && hi >= 0 {
		return Form{}, ErrZeroDivisor
	}
	alpha := -1 / (lo * hi)
	// Deviation d(x) = 1/x - alpha*x is monotone between its critical
	// point x* = +-sqrt(lo*hi) and the endpoints; bound it exactly.
	cand := []float64{lo, hi}
	xc := math.Sqrt(lo * hi)
	if lo < 0 {
		xc = -xc
	}
	if xc > lo && xc < hi {
		cand = append(cand, xc)
	}
	dmin, dmax := math.Inf(1), math.Inf(-1)
	for _, x := range cand {
		d := 1/x - alpha*x
		if d < dmin {
			dmin = d
		}
		if d > dmax {
			dmax = d
		}
	}
	zeta := (dmin + dmax) / 2
	delta := (dmax - dmin) / 2
	h := f.Scale(alpha).Shift(zeta)
	h.addRem(delta)
	h.consolidate()
	return h, nil
}

// Div returns f / g, or ErrZeroDivisor if g's range contains zero.
func (f Form) Div(g Form) (Form, error) {
	sameEnv(f, g)
	r, err := g.Recip()
	if err != nil {
		return Form{}, err
	}
	return f.Mul(r), nil
}
