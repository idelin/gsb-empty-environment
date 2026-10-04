package motion

import "math"

// QuadRoot isolates one real root of the quadratic
//
//	A u^2 + B u + C = 0,   u = t - Offset,
//
// inside the bracket [Lo, Hi] (in local time u). It pinpoints a span
// endpoint that is not a trajectory breakpoint: the bracket can be refined
// by bisection so endpoints can be ordered and compared without floating
// point ambiguity. A degenerate bracket Lo == Hi represents a known exact
// root (e.g. a tangency point, where the root has no sign change).
type QuadRoot struct {
	A, B, C float64
	Offset  float64
	Lo, Hi  float64
}

func (q QuadRoot) eval(u float64) float64 { return (q.A*u+q.B)*u + q.C }

// Float64 refines the isolating bracket by bisection until it collapses to
// adjacent float64 values, and returns the root in absolute time.
func (q QuadRoot) Float64() float64 {
	lo, hi := q.Lo, q.Hi
	flo := q.eval(lo)
	for {
		mid := lo + (hi-lo)/2
		if mid == lo || mid == hi {
			return q.Offset + mid
		}
		fm := q.eval(mid)
		if fm == 0 {
			return q.Offset + mid
		}
		if (fm > 0) == (flo > 0) {
			lo, flo = mid, fm
		} else {
			hi = mid
		}
	}
}

// solvePair returns the spans of { t in [lo, hi) : au*u^2 + bu*u + c0 <= 0 }
// with u = t - lo and au >= 0. Breakpoint ends reuse lo/hi exactly so that
// adjacent pairs share identical endpoint floats for merging.
func solvePair(au, bu, c0, lo, hi float64) []Span {
	dur := hi - lo
	if !(dur > 0) {
		return nil
	}
	full := Span{Start: lo, End: hi, StartClosed: true, EndClosed: false}

	if au == 0 {
		// au = |dv|^2 == 0 means the relative velocity is the zero
		// vector, hence bu == 0 as well: the squared distance is
		// constant over the whole pair.
		if c0 <= 0 {
			return []Span{full}
		}
		return nil
	}

	// au > 0: upward parabola, the solution lies between the roots.
	d := bu*bu - 4*au*c0
	switch {
	case d < 0:
		return nil
	case d == 0:
		// Grazing contact: a single instant at the vertex.
		u := -bu / (2 * au)
		if u < 0 || u >= dur {
			return nil
		}
		r := &QuadRoot{A: au, B: bu, C: c0, Offset: lo, Lo: u, Hi: u}
		t := lo + u
		return []Span{Span{
			Start: t, End: t,
			StartClosed: true, EndClosed: true,
			StartRoot: r, EndRoot: r,
		}}
	}

	s := math.Sqrt(d)
	var u1, u2 float64
	switch {
	case c0 == 0:
		// Roots are 0 and -bu/au; avoid the 0/0 of the product form.
		u1, u2 = 0, -bu/au
		if u1 > u2 {
			u1, u2 = u2, u1
		}
	case bu >= 0:
		u1 = (-bu - s) / (2 * au)
		u2 = c0 / (au * u1)
	default:
		u2 = (-bu + s) / (2 * au)
		u1 = c0 / (au * u2)
	}

	if u2 < 0 || u1 >= dur {
		return nil
	}
	var span Span
	if u1 > 0 {
		span.Start = lo + u1
		span.StartClosed = true
		span.StartRoot = newRoot(au, bu, c0, u1, lo, dur)
	} else {
		span.Start = lo
		span.StartClosed = true
	}
	if u2 < dur {
		span.End = lo + u2
		span.EndClosed = true
		span.EndRoot = newRoot(au, bu, c0, u2, lo, dur)
	} else {
		span.End = hi
		span.EndClosed = false
	}
	if span.Start > span.End {
		return nil
	}
	if span.Start == span.End && !(span.StartClosed && span.EndClosed) {
		return nil
	}
	return []Span{span}
}

// newRoot builds a QuadRoot for the root near u (local time), isolating it
// inside [0, dur].
func newRoot(a, b, c, u, offset, dur float64) *QuadRoot {
	lo, hi := isolateRoot(a, b, c, u, dur)
	return &QuadRoot{A: a, B: b, C: c, Offset: offset, Lo: lo, Hi: hi}
}

// isolateRoot returns a bracket around u that contains exactly one root of
// a*u^2 + b*u + c, verified by a sign change. Falls back to the degenerate
// bracket [u, u] if no sign change can be established.
func isolateRoot(a, b, c, u, dur float64) (float64, float64) {
	f := func(x float64) float64 { return (a*x+b)*x + c }
	rad := math.Max(math.Abs(u), 1) * 1e-9
	for k := 0; k < 16; k++ {
		l := u - rad
		if l < 0 {
			l = 0
		}
		h := u + rad
		if h > dur {
			h = dur
		}
		fl, fh := f(l), f(h)
		if fl == 0 {
			return l, l
		}
		if fh == 0 {
			return h, h
		}
		if (fl > 0) != (fh > 0) {
			return l, h
		}
		rad *= 16
	}
	return u, u
}
