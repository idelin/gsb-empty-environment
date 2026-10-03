package sdf

// Hit2 describes the outcome of a 2D ray march.
type Hit2 struct {
	Hit      bool    // whether the ray reached a surface within MaxDist
	Point    Vec2    // final ray position (surface point when Hit)
	Distance float64 // total distance travelled along the ray
	Steps    int     // number of sphere-tracing steps taken
}

// Hit3 describes the outcome of a 3D ray march.
type Hit3 struct {
	Hit      bool    // whether the ray reached a surface within MaxDist
	Point    Vec3    // final ray position (surface point when Hit)
	Distance float64 // total distance travelled along the ray
	Steps    int     // number of sphere-tracing steps taken
}

// MarchOptions tunes the ray marcher. The zero value is valid and uses
// sensible defaults.
type MarchOptions struct {
	Epsilon  float64 // surface threshold; default 1e-4
	MaxDist  float64 // far plane; default 100
	MaxSteps int     // step cap; default 256
}

func (o MarchOptions) withDefaults() MarchOptions {
	if o.Epsilon <= 0 {
		o.Epsilon = 1e-4
	}
	if o.MaxDist <= 0 {
		o.MaxDist = 100
	}
	if o.MaxSteps <= 0 {
		o.MaxSteps = 256
	}
	return o
}

// RayMarch2 sphere-traces a ray from origin along dir (need not be
// normalized) against the signed distance field. Each step advances by
// exactly the field value at the current point, which is the largest
// distance guaranteed not to cross any surface, so the march converges
// in very few steps.
func RayMarch2(sdf SDF2, origin, dir Vec2, opt MarchOptions) Hit2 {
	o := opt.withDefaults()
	d := dir.Norm()
	t := 0.0
	for i := 0; i < o.MaxSteps; i++ {
		p := origin.Add(d.Mul(t))
		dist := sdf(p)
		if dist < o.Epsilon {
			return Hit2{Hit: true, Point: p, Distance: t, Steps: i + 1}
		}
		t += dist
		if t > o.MaxDist {
			break
		}
	}
	return Hit2{Hit: false, Point: origin.Add(d.Mul(t)), Distance: t, Steps: o.MaxSteps}
}

// RayMarch3 sphere-traces a ray from origin along dir (need not be
// normalized) against the signed distance field. See RayMarch2 for the
// stepping rule.
func RayMarch3(sdf SDF3, origin, dir Vec3, opt MarchOptions) Hit3 {
	o := opt.withDefaults()
	d := dir.Norm()
	t := 0.0
	for i := 0; i < o.MaxSteps; i++ {
		p := origin.Add(d.Mul(t))
		dist := sdf(p)
		if dist < o.Epsilon {
			return Hit3{Hit: true, Point: p, Distance: t, Steps: i + 1}
		}
		t += dist
		if t > o.MaxDist {
			break
		}
	}
	return Hit3{Hit: false, Point: origin.Add(d.Mul(t)), Distance: t, Steps: o.MaxSteps}
}

// RayMarchGrid2 sphere-traces a ray against a rasterized Grid2 by
// sampling the interpolated field. Because a sampled grid is only an
// approximation of the true SDF, a small safety factor is applied to
// each step to avoid overshooting between nodes.
func RayMarchGrid2(g *Grid2, origin, dir Vec2, opt MarchOptions) Hit2 {
	const safety = 0.9
	o := opt.withDefaults()
	d := dir.Norm()
	t := 0.0
	for i := 0; i < o.MaxSteps; i++ {
		p := origin.Add(d.Mul(t))
		dist := g.Sample(p)
		if dist < o.Epsilon {
			return Hit2{Hit: true, Point: p, Distance: t, Steps: i + 1}
		}
		t += dist * safety
		if t > o.MaxDist {
			break
		}
	}
	return Hit2{Hit: false, Point: origin.Add(d.Mul(t)), Distance: t, Steps: o.MaxSteps}
}

// RayMarchGrid3 sphere-traces a ray against a rasterized Grid3 by
// sampling the interpolated field. See RayMarchGrid2.
func RayMarchGrid3(g *Grid3, origin, dir Vec3, opt MarchOptions) Hit3 {
	const safety = 0.9
	o := opt.withDefaults()
	d := dir.Norm()
	t := 0.0
	for i := 0; i < o.MaxSteps; i++ {
		p := origin.Add(d.Mul(t))
		dist := g.Sample(p)
		if dist < o.Epsilon {
			return Hit3{Hit: true, Point: p, Distance: t, Steps: i + 1}
		}
		t += dist * safety
		if t > o.MaxDist {
			break
		}
	}
	return Hit3{Hit: false, Point: origin.Add(d.Mul(t)), Distance: t, Steps: o.MaxSteps}
}
