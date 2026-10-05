package motion

import "math"

// solvePiece solves |r0 + rv*s|^2 <= maxDistSq for local time s in the
// half-open domain [0, L), where L = hi-lo, r0 is the relative position at
// time lo and rv the (constant) relative velocity on the piece. It returns
// the solution set as a single interval in absolute time (the set is always
// connected because the quadratic opens upward), plus false when empty.
//
// A tangential (grazing) contact yields a point interval with Start == End
// and EndClosed == true. The end of the result is open exactly when the
// solution runs to the piece boundary hi, because hi itself belongs to the
// next piece.
func solvePiece(sa, sb Segment, lo, hi, maxDistSq float64) (Interval, bool) {
	dim := len(sa.Pos)
	r0 := make(Vec, dim)
	rv := make(Vec, dim)
	for d := 0; d < dim; d++ {
		pa := sa.Pos[d] + sa.Vel[d]*(lo-sa.Start)
		pb := sb.Pos[d] + sb.Vel[d]*(lo-sb.Start)
		r0[d] = pa - pb
		rv[d] = sa.Vel[d] - sb.Vel[d]
	}
	// f(s) = a2*s^2 + b1*s + c0 <= 0
	var a2, b1, c0 float64
	for d := 0; d < dim; d++ {
		a2 += rv[d] * rv[d]
		b1 += 2 * r0[d] * rv[d]
		c0 += r0[d] * r0[d]
	}
	c0 -= maxDistSq
	L := hi - lo

	// a2 == 0 iff rv == 0, which also forces b1 == 0: the squared distance
	// is constant on the piece (both bodies静止 or co-moving).
	if a2 == 0 {
		if c0 <= 0 {
			return Interval{Start: lo, End: hi, EndClosed: false}, true
		}
		return Interval{}, false
	}

	disc := b1*b1 - 4*a2*c0
	if disc < 0 {
		return Interval{}, false
	}
	if disc == 0 {
		// Tangential contact: f touches zero at a single instant.
		s0 := -b1 / (2 * a2)
		if s0 >= 0 && s0 < L {
			t := lo + s0
			return Interval{Start: t, End: t, EndClosed: true}, true
		}
		return Interval{}, false
	}

	// Two distinct roots; f <= 0 on the closed interval between them.
	// Numerically stable root computation (q-form), then ordered.
	sq := math.Sqrt(disc)
	q := -0.5 * (b1 + math.Copysign(sq, b1))
	r1 := q / a2
	r2 := c0 / q
	s1 := math.Min(r1, r2)
	s2 := math.Max(r1, r2)

	// Intersect [s1, s2] with the half-open domain [0, L).
	loS := math.Max(s1, 0)
	hiS := math.Min(s2, L)
	if loS > hiS {
		return Interval{}, false
	}
	if loS == hiS {
		// A single touching point, kept only if it lies inside the domain
		// (a root exactly at L belongs to the next piece, not this one).
		if loS < L {
			t := lo + loS
			return Interval{Start: t, End: t, EndClosed: true}, true
		}
		return Interval{}, false
	}
	return Interval{
		Start: lo + loS,
		End:   lo + hiS,
		// Closed only when the exit root lies strictly inside the piece;
		// at the piece boundary the next segment decides.
		EndClosed: s2 < L,
	}, true
}
