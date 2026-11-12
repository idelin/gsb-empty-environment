package taskpkg

import "math"

const (
	// defaultEps is used when the caller does not supply a positive tolerance.
	defaultEps = 1e-10
	// maxBisections bounds inversion work even when eps is extremely small.
	maxBisections = 200
)

// Sampler importance-samples directions from a cube environment map. Each
// pixel receives discrete weight luminance * exact solid angle; after a
// pixel is selected by the hierarchical distribution, the direction is drawn
// uniformly in solid angle over that pixel's spherical rectangle, so the
// returned density is a proper PDF with respect to sphere area (dOmega).
type Sampler struct {
	cube    *CubeMap
	dist    *stratifiedDistribution
	eps     float64
	uniform bool
}

// NewSampler builds the sampling structure for cube. eps is the caller-given
// error tolerance: the monotone CDF inversions stop once the bracket width
// is at most eps, hence sampled coordinates reproduce the solid-angle area
// distribution within that tolerance. A non-positive eps selects a default.
// If every pixel is black (total weight zero), the sampler falls back to a
// uniform distribution over the sphere.
func NewSampler(cube *CubeMap, eps float64) *Sampler {
	if cube == nil {
		panic("taskpkg: NewSampler requires a non-nil cube map")
	}
	n := cube.Resolution()
	weights := make([]float64, FaceCount*n*n)
	leaf := 0
	for face := 0; face < FaceCount; face++ {
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				weights[leaf] = cube.Luminance(face, i, j) * cube.SolidAngle(face, i, j)
				leaf++
			}
		}
	}
	if eps <= 0 {
		eps = defaultEps
	}
	s := &Sampler{cube: cube, eps: eps}
	if sumWeights(weights) == 0 {
		s.uniform = true
		return s
	}
	s.dist = newStratifiedDistribution(weights)
	return s
}

func sumWeights(weights []float64) float64 {
	total := 0.0
	for _, weight := range weights {
		total += weight
	}
	return total
}

// Eps reports the tolerance used for CDF inversion.
func (s *Sampler) Eps() float64 { return s.eps }

// Uniform reports whether the all-black fallback (uniform sphere sampling)
// is active.
func (s *Sampler) Uniform() bool { return s.uniform }

// Integral returns the sphere-area integral of luminance, sum over pixels of
// luminance * solid angle. It is zero for an all-black cube map.
func (s *Sampler) Integral() float64 {
	if s.uniform {
		return 0
	}
	return s.dist.total()
}

// PixelWeight returns the discrete weight luminance * solid angle of a pixel.
func (s *Sampler) PixelWeight(face, i, j int) float64 {
	if s.uniform {
		return 0
	}
	leaf := face*s.cube.Resolution()*s.cube.Resolution() + j*s.cube.Resolution() + i
	return s.dist.weightAt(leaf)
}

// Sample draws one direction. The three independent random numbers xi1, xi2,
// xi3 in [0, 1) drive pixel selection and the two solid-angle CDF inversions.
// It returns the unit direction and the PDF with respect to solid angle
// dOmega; the PDF is strictly positive for every lit pixel and never zero for
// any direction that can be sampled.
func (s *Sampler) Sample(xi1, xi2, xi3 float64) (direction Vec3, pdf float64) {
	if s.uniform {
		return sampleUniformSphere(xi1, xi2), 1 / (4 * math.Pi)
	}

	leaf, discreteProbability := s.dist.sample(xi1)
	face, i, j := s.leafLocation(leaf)
	u0, u1, v0, v1 := s.cube.pixelBounds(i, j)

	// Invert the marginal CDF over u: G(u) is proportional to the solid-angle
	// mass of the rectangle [v0, v1] x [u0, u].
	marginal := func(u float64) float64 {
		return rectPrimitive(v1, u) - rectPrimitive(v0, u) -
			rectPrimitive(v1, u0) + rectPrimitive(v0, u0)
	}
	marginalTarget := clampUnit(xi2) * marginal(u1)
	marginalTarget = clampRange(marginalTarget, 0, marginal(u1))
	u := invertMonotone(marginal, u0, u1, marginalTarget, s.eps)

	// Invert the conditional CDF over v given u. K is zero at v0 so the
	// bisection target always lies inside the bracketed function range even
	// when H(v0, u) itself is non-zero. K can be negative for u < 0 because
	// H is odd in u as well, so clamp to the actual (signed) endpoint.
	conditional := func(v float64) float64 {
		return rectPrimitive(v, u) - rectPrimitive(v0, u)
	}
	conditionalTarget := clampUnit(xi3) * conditional(v1)
	conditionalTarget = clampRange(conditionalTarget, 0, conditional(v1))
	v := invertMonotone(conditional, v0, v1, conditionalTarget, s.eps)

	direction = faces[face].direction(u, v)

	// Discrete pixel probability divided by the pixel's solid angle gives the
	// area density; direction generation inside the pixel is solid-angle
	// uniform, hence a constant pdf over the pixel.
	pixelSolidAngle := solidAngleRect(u0, u1, v0, v1)
	pdf = discreteProbability / pixelSolidAngle
	return direction, pdf
}

// PDF evaluates the area density (per unit solid angle) of the sampling
// distribution at an arbitrary unit direction. Directions on seams or corners
// follow the fixed face priority; lit pixels yield a positive PDF, black
// pixels yield zero. The all-black fallback returns 1 / (4*pi) everywhere.
func (s *Sampler) PDF(direction Vec3) float64 {
	if s.uniform {
		return 1 / (4 * math.Pi)
	}
	face, u, v := locateDirection(direction)
	n := s.cube.Resolution()
	i := coordinateIndex(u, n)
	j := coordinateIndex(-v, n)

	leaf := face*n*n + j*n + i
	weight := s.dist.weightAt(leaf)
	if weight <= 0 {
		return 0
	}
	pixelSolidAngle := s.cube.SolidAngle(face, i, j)
	return weight / (s.dist.total() * pixelSolidAngle)
}

func (s *Sampler) leafLocation(leaf int) (face, i, j int) {
	n := s.cube.Resolution()
	face = leaf / (n * n)
	rem := leaf % (n * n)
	i = rem % n
	j = rem / n
	return face, i, j
}

// rectPrimitive is H(v, u), an antiderivative of the solid-angle element:
// d/du H(v, u) = v / ((1 + v^2 + u^2) * sqrt(1 + v^2 + u^2)).
// H is odd in v and H(0, u) = 0, so pixels crossing v = 0 stay consistent.
func rectPrimitive(v, u float64) float64 {
	return math.Atan(v * u / math.Sqrt(1+v*v))
}

// invertMonotone solves f(x) = target for a continuous monotone f on
// [low, high], bisection until the bracket is no wider than eps. Both
// non-decreasing and non-increasing f are supported (the sign is detected
// from the endpoints). The residual area error of the returned coordinate is
// bounded by eps through f's Lipschitz behavior on the bounded cube domain.
func invertMonotone(f func(float64) float64, low, high, target, eps float64) float64 {
	decreasing := f(high) < f(low)
	for iteration := 0; iteration < maxBisections; iteration++ {
		if high-low <= eps {
			break
		}
		mid := (low + high) / 2
		value := f(mid)
		if (!decreasing && value < target) || (decreasing && value > target) {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func clampUnit(xi float64) float64 {
	if xi < 0 {
		return 0
	}
	if xi >= 1 {
		return 1 - math.SmallestNonzeroFloat64
	}
	return xi
}

// clampRange clamps value into the interval bounded by a and b, regardless of
// which endpoint is larger. CDF spans may be signed because rectPrimitive is
// odd in both arguments.
func clampRange(value, a, b float64) float64 {
	low, high := a, b
	if b < a {
		low, high = b, a
	}
	return math.Max(low, math.Min(high, value))
}

func coordinateIndex(value float64, n int) int {
	index := int((value + 1) * float64(n) / 2)
	if index < 0 {
		return 0
	}
	if index >= n {
		return n - 1
	}
	return index
}

// locateDirection maps a unit direction to its owning face and canonical
// (u, v) coordinates. The face whose normal has the largest dot product wins;
// exact ties on a seam or corner keep the earlier face in the fixed priority
// order, because the loop only replaces a strictly better face.
func locateDirection(direction Vec3) (face int, u, v float64) {
	bestDot := math.Inf(-1)
	for candidate := 0; candidate < FaceCount; candidate++ {
		dot := direction.Dot(faces[candidate].normal)
		if dot > bestDot {
			bestDot, face = dot, candidate
		}
	}
	frame := faces[face]
	dn := direction.Dot(frame.normal)
	u = direction.Dot(frame.uAxis) / dn
	v = direction.Dot(frame.vAxis) / dn
	u = math.Max(-1, math.Min(1, u))
	v = math.Max(-1, math.Min(1, v))
	return face, u, v
}

// sampleUniformSphere draws cosine-free uniform directions on the sphere:
// z is uniform in [-1, 1] and phi uniform in [0, 2*pi), density 1 / (4*pi).
func sampleUniformSphere(xi1, xi2 float64) Vec3 {
	z := 1 - 2*clampUnit(xi1)
	radius := math.Sqrt(math.Max(0, 1-z*z))
	phi := 2 * math.Pi * clampUnit(xi2)
	return Vec3{radius * math.Cos(phi), radius * math.Sin(phi), z}
}
