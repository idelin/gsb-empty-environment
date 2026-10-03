package sdf

import "math"

// Mesh is a triangle mesh exposed as a signed distance field. The
// unsigned distance is the distance to the closest point on any
// triangle; the sign is decided by the generalized winding number
// (sum of solid angles), so it is robust for closed meshes.
type Mesh struct {
	Tris []Triangle
}

// NewMesh builds a mesh SDF from triangles.
func NewMesh(tris []Triangle) *Mesh { return &Mesh{Tris: tris} }

// Distance implements SDF3.
func (m *Mesh) Distance(p Vec3) float64 {
	best := math.Inf(1)
	winding := 0.0
	for _, t := range m.Tris {
		d := p.Sub(closestPointTriangle(p, t.A, t.B, t.C)).LenSq()
		if d < best {
			best = d
		}
		winding += solidAngle(p, t.A, t.B, t.C)
	}
	dist := math.Sqrt(best)
	// |winding| = 4*pi inside a closed surface, ~0 outside. Abs makes
	// the sign independent of the input triangle winding convention.
	if math.Abs(winding) > 2*math.Pi {
		return -dist
	}
	return dist
}

// closestPointTriangle returns the closest point on triangle abc to p
// (Christer Ericson, Real-Time Collision Detection).
func closestPointTriangle(p, a, b, c Vec3) Vec3 {
	ab := b.Sub(a)
	ac := c.Sub(a)
	ap := p.Sub(a)
	d1 := ab.Dot(ap)
	d2 := ac.Dot(ap)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bp := p.Sub(b)
	d3 := ab.Dot(bp)
	d4 := ac.Dot(bp)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		return a.Add(ab.Scale(d1 / (d1 - d3)))
	}
	cp := p.Sub(c)
	d5 := ab.Dot(cp)
	d6 := ac.Dot(cp)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		return a.Add(ac.Scale(d2 / (d2 - d6)))
	}
	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return b.Add(c.Sub(b).Scale(w))
	}
	denom := 1 / (va + vb + vc)
	return a.Add(ab.Scale(vb * denom)).Add(ac.Scale(vc * denom))
}

// solidAngle returns the signed solid angle subtended by triangle abc
// as seen from p (van Oosterom & Strackee).
func solidAngle(p, a, b, c Vec3) float64 {
	va := a.Sub(p)
	vb := b.Sub(p)
	vc := c.Sub(p)
	la, lb, lc := va.Len(), vb.Len(), vc.Len()
	num := va.Dot(vb.Cross(vc))
	den := la*lb*lc + va.Dot(vb)*lc + vb.Dot(vc)*la + vc.Dot(va)*lb
	return 2 * math.Atan2(num, den)
}

// CubeMesh returns a closed 12-triangle cube mesh centered at c with
// the given half-extent. Useful for tests and demos.
func CubeMesh(c Vec3, half float64) *Mesh {
	v := [8]Vec3{
		{-half, -half, -half}, {half, -half, -half},
		{half, half, -half}, {-half, half, -half},
		{-half, -half, half}, {half, -half, half},
		{half, half, half}, {-half, half, half},
	}
	for i := range v {
		v[i] = v[i].Add(c)
	}
	quad := func(a, b, c, d Vec3) []Triangle {
		return []Triangle{{a, b, c}, {a, c, d}}
	}
	var tris []Triangle
	tris = append(tris, quad(v[0], v[1], v[2], v[3])...) // -z
	tris = append(tris, quad(v[5], v[4], v[7], v[6])...) // +z
	tris = append(tris, quad(v[4], v[0], v[3], v[7])...) // -x
	tris = append(tris, quad(v[1], v[5], v[6], v[2])...) // +x
	tris = append(tris, quad(v[4], v[5], v[1], v[0])...) // -y
	tris = append(tris, quad(v[3], v[2], v[6], v[7])...) // +y
	return NewMesh(tris)
}
