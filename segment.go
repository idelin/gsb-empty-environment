package taskpkg

import "math"

// RGB is a linear-space, unclamped RGB value (emission rates or radiance).
type RGB [3]float64

// Vec3 is a 3D point or direction.
type Vec3 [3]float64

// Segment is the analytic, constant-coefficient solution of the
// emission-absorption radiative transfer equation
//
//	dL/ds + sigma*L = em(s)
//
// over a path of length h for fixed sigma and fixed emission em:

//	transmittance  T = exp(-sigma*h)
//	emitted radiance C = em * (1 - T) / sigma,
//
// composed front-to-back as L' = L + T*C, T' = T*T.
type Segment struct {
	T float64
	C RGB
}

// analyticSegment evaluates the constant-coefficient solution for one
// segment. The zero-absorption case is evaluated as its exact limit
// C = em*h, T = 1, avoiding the 0/0 form and its numerical instability.
func analyticSegment(sigma float64, em RGB, h float64) Segment {
	if h <= 0 || (sigma == 0 && em == RGB{}) {
		return Segment{T: 1}
	}
	if sigma <= 0 {
		return Segment{T: 1, C: RGB{em[0] * h, em[1] * h, em[2] * h}}
	}
	// expm1 keeps 1-exp(-x) accurate for very short optical depths.
	oneMinusT := -math.Expm1(-sigma * h)
	if oneMinusT < 0 {
		oneMinusT = 0
	}
	k := oneMinusT / sigma
	return Segment{
		T: 1 - oneMinusT,
		C: RGB{em[0] * k, em[1] * k, em[2] * k},
	}
}

// compose merges segment b behind segment a: the radiance of b reaches the
// front of a attenuated by a's transmittance.
func compose(a, b Segment) Segment {
	return Segment{
		T: a.T * b.T,
		C: RGB{
			a.C[0] + a.T*b.C[0],
			a.C[1] + a.T*b.C[1],
			a.C[2] + a.T*b.C[2],
		},
	}
}

func absRGB(v RGB) RGB {
	return RGB{math.Abs(v[0]), math.Abs(v[1]), math.Abs(v[2])}
}

func maxRGB(a, b RGB) RGB {
	return RGB{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}
