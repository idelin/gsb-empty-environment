package taskpkg

import "math"

// cubicRootsOpen01 returns every real root of
// c3 t^3 + c2 t^2 + c1 t + c0 = 0 lying in the open interval (0,1).
// Lower-degree equations are handled by exact zero coefficient checks, so
// degenerate quadratics (straight-line curves) are safe. Analytic roots are
// polished with Newton steps on the original polynomial for accuracy.
func cubicRootsOpen01(c3, c2, c1, c0 float64) []float64 {
	var roots []float64
	switch {
	case c3 == 0 && c2 == 0:
		if c1 == 0 {
			return nil
		}
		roots = []float64{-c0 / c1}
	case c3 == 0:
		roots = quadRoots(c2, c1, c0)
	default:
		roots = cubicRoots(c3, c2, c1, c0)
	}
	out := roots[:0]
	for _, r := range roots {
		t := polishCubic(c3, c2, c1, c0, r)
		if t <= 0 || t >= 1 {
			continue
		}
		dup := false
		for _, u := range out {
			if math.Abs(t-u) < 1e-9 {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, t)
		}
	}
	return out
}

// cubicRoots returns the real roots of a t^3 + b t^2 + c t + d = 0 with a != 0.
func cubicRoots(a, b, c, d float64) []float64 {
	// Depress the cubic: t = x - b/(3a) gives x^3 + p x + q = 0.
	inv := 1 / a
	b, c, d = b*inv, c*inv, d*inv
	p := c - b*b/3
	q := d + b*(2*b*b-9*c)/27
	off := b / 3

	qs := q * q / 4
	ps := p * p * p / 27
	disc := qs + ps
	tol := 1e-12 * math.Max(qs, math.Abs(ps))
	switch {
	case disc > tol:
		// One real root (Cardano).
		s := math.Sqrt(disc)
		return []float64{math.Cbrt(-q/2+s) + math.Cbrt(-q/2-s) - off}
	case disc < -tol:
		// Three real roots (trigonometric method, numerically stable).
		m := 2 * math.Sqrt(-p/3)
		theta := math.Acos(clamp(3*q/(p*m), -1, 1)) / 3
		return []float64{
			m*math.Cos(theta) - off,
			m*math.Cos(theta-2*math.Pi/3) - off,
			m*math.Cos(theta-4*math.Pi/3) - off,
		}
	default:
		// Multiple root.
		u := math.Cbrt(-q / 2)
		return []float64{2*u - off, -u - off}
	}
}

// quadRoots returns the real roots of a t^2 + b t + c = 0 with a != 0.
func quadRoots(a, b, c float64) []float64 {
	disc := b*b - 4*a*c
	if disc < 0 {
		return nil
	}
	if disc == 0 {
		return []float64{-b / (2 * a)}
	}
	s := math.Sqrt(disc)
	q := -0.5 * (b + math.Copysign(s, b))
	if q == 0 {
		return []float64{0}
	}
	return []float64{q / a, c / q}
}

// polishCubic refines a root estimate with guarded Newton iterations.
func polishCubic(c3, c2, c1, c0, t float64) float64 {
	for i := 0; i < 4; i++ {
		f := ((c3*t+c2)*t+c1)*t + c0
		df := (3*c3*t+2*c2)*t + c1
		if df == 0 {
			break
		}
		t -= f / df
	}
	return t
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
