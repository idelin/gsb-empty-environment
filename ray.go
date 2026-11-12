package taskpkg

import "math"

// Ray is a half-line with an unnormalised direction. Parameters are measured
// in units of Direction, so PointAt(t) = Origin + t*Direction and a hit's T is
// directly comparable across differently scaled instances.
type Ray struct {
	Origin    Vec3
	Direction Vec3
}

func NewRay(origin, direction Vec3) Ray {
	return Ray{Origin: origin, Direction: direction}
}

func (r Ray) PointAt(t float64) Vec3 {
	return r.Origin.Add(r.Direction.Mul(t))
}

// parameterOf returns the parameter of point on the ray in the original
// Direction units. The direction is rescaled during the projection so extreme
// ray direction magnitudes do not overflow LengthSquared.
func (r Ray) parameterOf(point Vec3) float64 {
	d := r.Direction
	maxComponent := math.Max(math.Abs(d.X), math.Max(math.Abs(d.Y), math.Abs(d.Z)))
	if maxComponent == 0 || !isFinite(maxComponent) {
		return math.NaN()
	}
	scaled := d.Mul(1 / maxComponent)
	denominator := scaled.LengthSquared() * maxComponent
	if denominator == 0 {
		return math.NaN()
	}
	return point.Sub(r.Origin).Dot(scaled) / denominator
}

// PickOptions controls a scene query.
//
// MaxT==0 means unbounded. TMin is inclusive and MaxT is inclusive.
// CullBackfaces discards back-facing triangles and CullFrontfaces discards
// front-facing triangles. Triangle winding follows the mesh: a triangle is
// front-facing when the ray looks at the side its vertices name as front.
// Mirrored instances (negative determinant) keep that logical front side, just
// as a renderer flips its front-face winding rule for a negative determinant.
type PickOptions struct {
	TMin           float64
	MaxT           float64
	CullBackfaces  bool
	CullFrontfaces bool
	// TieTolerance resolves nearly equal world parameters in favour of the
	// smaller (InstanceID, TriangleID). It is relative to max(1, |t|).
	// Zero uses the package default of 1e-12; set a negative value for exact
	// float equality.
	TieTolerance float64
}

const defaultTieTolerance = 1e-12

func (o PickOptions) normalized() PickOptions {
	if o.MaxT == 0 {
		o.MaxT = math.Inf(1)
	}
	if o.TieTolerance == 0 {
		o.TieTolerance = defaultTieTolerance
	}
	return o
}

func (o PickOptions) tieTolerance(t float64) float64 {
	if o.TieTolerance < 0 {
		return 0
	}
	scale := math.Max(1, math.Abs(t))
	return o.TieTolerance * scale
}
