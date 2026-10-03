package sdf

// Triangle is a triangle of an extracted 3D iso-surface or a mesh.
type Triangle struct {
	A, B, C Vec3
}

// Centroid returns the triangle's centroid.
func (t Triangle) Centroid() Vec3 {
	return t.A.Add(t.B).Add(t.C).Scale(1.0 / 3.0)
}

// Normal returns the (unnormalized) face normal via the right-hand rule.
func (t Triangle) Normal() Vec3 {
	return t.B.Sub(t.A).Cross(t.C.Sub(t.A))
}

// Area returns the triangle's area.
func (t Triangle) Area() float64 {
	return 0.5 * t.Normal().Len()
}

// tetrahedra decomposing a cube with corner indices
//
//	0:(0,0,0) 1:(1,0,0) 2:(1,1,0) 3:(0,1,0)
//	4:(0,0,1) 5:(1,0,1) 6:(1,1,1) 7:(0,1,1)
var tetrahedra = [6][4]int{
	{0, 5, 1, 6},
	{0, 1, 2, 6},
	{0, 2, 3, 6},
	{0, 3, 7, 6},
	{0, 7, 4, 6},
	{0, 4, 5, 6},
}

// Surface extracts the iso-surface (zero level set by default) of the
// rasterized 3D field using marching tetrahedra: every grid cell is
// split into six tetrahedra, each of which emits zero, one or two
// triangles. Triangle windings are oriented so normals point outward,
// toward the positive (outside) side of the field.
func (g *Grid3) Surface(iso float64) []Triangle {
	var tris []Triangle
	cornerI := [8][3]int{
		{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0},
		{0, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 1, 1},
	}
	for k := 0; k < g.Nz-1; k++ {
		for j := 0; j < g.Ny-1; j++ {
			for i := 0; i < g.Nx-1; i++ {
				var pos [8]Vec3
				var val [8]float64
				for c := 0; c < 8; c++ {
					ci, cj, ck := i+cornerI[c][0], j+cornerI[c][1], k+cornerI[c][2]
					pos[c] = g.Position(ci, cj, ck)
					val[c] = g.At(ci, cj, ck)
				}
				for _, tet := range tetrahedra {
					var tp [4]Vec3
					var tv [4]float64
					for n := 0; n < 4; n++ {
						tp[n] = pos[tet[n]]
						tv[n] = val[tet[n]]
					}
					tris = polygonizeTet(tp, tv, iso, tris)
				}
			}
		}
	}
	return tris
}

// tetEdge lists the six edges of a tetrahedron as endpoint index pairs.
var tetEdge = [6][2]int{
	{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3},
}

func edgePoint(p [4]Vec3, v [4]float64, e int, iso float64) Vec3 {
	a, b := tetEdge[e][0], tetEdge[e][1]
	t := (iso - v[a]) / (v[b] - v[a])
	return p[a].Lerp(p[b], t)
}

func polygonizeTet(p [4]Vec3, v [4]float64, iso float64, out []Triangle) []Triangle {
	mask := 0
	inside := Vec3{}
	nInside := 0
	for n := 0; n < 4; n++ {
		if v[n] < iso {
			mask |= 1 << uint(n)
			inside = inside.Add(p[n])
			nInside++
		}
	}
	if mask == 0 || mask == 15 {
		return out
	}
	inside = inside.Scale(1.0 / float64(nInside))

	var tris []Triangle
	single := func(e0, e1, e2 int) {
		tris = append(tris, Triangle{
			edgePoint(p, v, e0, iso),
			edgePoint(p, v, e1, iso),
			edgePoint(p, v, e2, iso),
		})
	}
	quad := func(e0, e1, e2, e3 int) {
		p0 := edgePoint(p, v, e0, iso)
		p1 := edgePoint(p, v, e1, iso)
		p2 := edgePoint(p, v, e2, iso)
		p3 := edgePoint(p, v, e3, iso)
		tris = append(tris, Triangle{p0, p1, p2}, Triangle{p0, p2, p3})
	}

	// Edge ids: 0:01 1:02 2:03 3:12 4:13 5:23
	switch mask {
	case 1, 14: // corner 0 vs the rest
		single(0, 1, 2)
	case 2, 13: // corner 1
		single(0, 3, 4)
	case 4, 11: // corner 2
		single(1, 3, 5)
	case 8, 7: // corner 3
		single(2, 4, 5)
	case 3, 12: // {0,1} vs {2,3}
		quad(1, 3, 4, 2)
	case 5, 10: // {0,2} vs {1,3}
		quad(0, 3, 5, 2)
	case 6, 9: // {1,2} vs {0,3}
		quad(0, 1, 5, 4)
	}

	// Orient triangles so normals point away from the inside region.
	for n := range tris {
		t := tris[n]
		if t.Normal().Dot(t.Centroid().Sub(inside)) < 0 {
			tris[n].B, tris[n].C = tris[n].C, tris[n].B
		}
	}
	return append(out, tris...)
}
