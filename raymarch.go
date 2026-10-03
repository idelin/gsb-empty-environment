package sdf

// MarchOptions controls sphere-tracing ray marching.
type MarchOptions struct {
	// MaxDist is the far plane; rays missing everything before it miss.
	// Zero selects the default of 100.
	MaxDist float64
	// Epsilon is the surface hit threshold. Zero selects 1e-3.
	Epsilon float64
	// MaxSteps bounds the iteration count. Zero selects 256.
	MaxSteps int
	// StepScale scales each step. Use 1.0 for exact (analytic) distance
	// fields; use a value slightly below 1.0 (e.g. 0.9) when marching a
	// rasterized grid, whose interpolated values can underestimate the
	// true distance near edges. Zero selects 1.0.
	StepScale float64
}

func (o MarchOptions) withDefaults() MarchOptions {
	if o.MaxDist <= 0 {
		o.MaxDist = 100
	}
	if o.Epsilon <= 0 {
		o.Epsilon = 1e-3
	}
	if o.MaxSteps <= 0 {
		o.MaxSteps = 256
	}
	if o.StepScale <= 0 {
		o.StepScale = 1
	}
	return o
}

// MarchResult2 reports the outcome of a 2D ray march.
type MarchResult2 struct {
	Hit      bool
	T        float64 // distance travelled along the ray
	Position Vec2    // world-space hit point (valid when Hit)
	Steps    int     // SDF evaluations performed
}

// RayMarch2 sphere-traces a ray from ro along unit direction rd through
// a 2D signed distance field. Because the SDF value at any point is a
// guaranteed lower bound on the distance to the nearest surface, each
// step safely skips that whole "empty sphere" of space, converging in
// very few iterations.
func RayMarch2(s SDF2, ro, rd Vec2, o MarchOptions) MarchResult2 {
	o = o.withDefaults()
	t := 0.0
	steps := 0
	for ; steps < o.MaxSteps; steps++ {
		p := ro.Add(rd.Scale(t))
		d := s.Distance(p)
		if d < o.Epsilon {
			return MarchResult2{Hit: true, T: t, Position: p, Steps: steps + 1}
		}
		t += d * o.StepScale
		if t > o.MaxDist {
			break
		}
	}
	return MarchResult2{Hit: false, T: t, Steps: steps}
}

// MarchResult3 reports the outcome of a 3D ray march.
type MarchResult3 struct {
	Hit      bool
	T        float64 // distance travelled along the ray
	Position Vec3    // world-space hit point (valid when Hit)
	Steps    int     // SDF evaluations performed
}

// RayMarch3 sphere-traces a ray from ro along unit direction rd through
// a 3D signed distance field. See RayMarch2 for the stepping rule.
func RayMarch3(s SDF3, ro, rd Vec3, o MarchOptions) MarchResult3 {
	o = o.withDefaults()
	t := 0.0
	steps := 0
	for ; steps < o.MaxSteps; steps++ {
		p := ro.Add(rd.Scale(t))
		d := s.Distance(p)
		if d < o.Epsilon {
			return MarchResult3{Hit: true, T: t, Position: p, Steps: steps + 1}
		}
		t += d * o.StepScale
		if t > o.MaxDist {
			break
		}
	}
	return MarchResult3{Hit: false, T: t, Steps: steps}
}

// Normal2 estimates the outward surface normal of a 2D field at p via
// central differences of the SDF gradient.
func Normal2(s SDF2, p Vec2) Vec2 {
	const h = 1e-4
	gx := s.Distance(Vec2{p.X + h, p.Y}) - s.Distance(Vec2{p.X - h, p.Y})
	gy := s.Distance(Vec2{p.X, p.Y + h}) - s.Distance(Vec2{p.X, p.Y - h})
	return Vec2{gx, gy}.Normalize()
}

// Normal3 estimates the outward surface normal of a 3D field at p via
// central differences of the SDF gradient.
func Normal3(s SDF3, p Vec3) Vec3 {
	const h = 1e-4
	gx := s.Distance(Vec3{p.X + h, p.Y, p.Z}) - s.Distance(Vec3{p.X - h, p.Y, p.Z})
	gy := s.Distance(Vec3{p.X, p.Y + h, p.Z}) - s.Distance(Vec3{p.X, p.Y - h, p.Z})
	gz := s.Distance(Vec3{p.X, p.Y, p.Z + h}) - s.Distance(Vec3{p.X, p.Y, p.Z - h})
	return Vec3{gx, gy, gz}.Normalize()
}
