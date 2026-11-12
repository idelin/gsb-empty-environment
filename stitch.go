package taskpkg

import "sort"

// PointKind classifies the vertices of the stitched triangle mesh.
type PointKind uint8

const (
	// CornerPoint is an original mesh vertex, identified by vertex ID.
	CornerPoint PointKind = iota
	// EdgePoint lies on a shared edge, uniquely identified by the edge ID
	// and a rational parameter Num/Den measured from Edge.Lo to Edge.Hi.
	EdgePoint
	// InteriorPoint lies inside one patch, identified by patch ID and
	// integer grid coordinates.
	InteriorPoint
	// CenterPoint is the center of one grid cell of a patch, introduced
	// as a fan apex when every cell corner touches a subdivided edge.
	CenterPoint
)

// PointID deterministically and uniquely identifies a generated vertex.
// Two patches sharing an edge derive identical PointIDs (and therefore
// bitwise identical positions) for their common boundary points.
type PointID struct {
	Kind PointKind
	// CornerPoint: the vertex ID.
	Vert int
	// EdgePoint: the shared edge and the rational parameter along it,
	// measured from Edge.Lo towards Edge.Hi. Den is a power of two.
	Edge EdgeID
	Num  int
	Den  int
	// InteriorPoint: the owning patch and its grid coordinates.
	Patch int
	I     int
	J     int
}

// Triangle is a counter-clockwise triple of point IDs.
type Triangle [3]PointID

// PatchMesh is the triangulation of a single patch.
type PatchMesh struct {
	PatchID   int
	Points    map[PointID]Vec3
	Triangles []Triangle
}

// Stitcher triangulates a Topology into crack-free patch meshes.
type Stitcher struct {
	topo      *Topology
	cornerPos func(vert int) Vec3
	eval      func(patchID int, u, v float64) Vec3
}

// NewStitcher builds a Stitcher. cornerPos returns the position of a mesh
// vertex. eval evaluates a patch interior at parameters (u, v) in [0,1];
// if nil, bilinear interpolation of the patch corners is used.
func NewStitcher(t *Topology, cornerPos func(vert int) Vec3, eval func(patchID int, u, v float64) Vec3) *Stitcher {
	if eval == nil {
		eval = func(patchID int, u, v float64) Vec3 {
			p := t.patches[t.patchIdx[patchID]]
			c := [4]Vec3{
				cornerPos(p.Corners[0]),
				cornerPos(p.Corners[1]),
				cornerPos(p.Corners[2]),
				cornerPos(p.Corners[3]),
			}
			w := [4]float64{(1 - u) * (1 - v), u * (1 - v), u * v, (1 - u) * v}
			var out Vec3
			for k := 0; k < 4; k++ {
				out = out.Add(c[k].Scale(w[k]))
			}
			return out
		}
	}
	return &Stitcher{topo: t, cornerPos: cornerPos, eval: eval}
}

// Stitch propagates level constraints (idempotently) and triangulates
// every patch. Patches are processed in ascending patch ID order, so the
// output is deterministic.
func (s *Stitcher) Stitch() []PatchMesh {
	s.topo.PropagateLevels()
	idx := make([]int, len(s.topo.patches))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return s.topo.patches[idx[a]].ID < s.topo.patches[idx[b]].ID })
	out := make([]PatchMesh, 0, len(idx))
	for _, i := range idx {
		out = append(out, s.stitchPatch(i))
	}
	return out
}

// stitchPatch triangulates one patch as a regular 1<<L grid, inserting
// edge midpoints and transition triangles along any boundary edge whose
// neighbour is one level finer.
func (s *Stitcher) stitchPatch(pi int) PatchMesh {
	p := s.topo.patches[pi]
	L := p.Level
	N := 1 << L
	pm := PatchMesh{PatchID: p.ID, Points: map[PointID]Vec3{}}

	var eLevel [4]int
	for e := 0; e < 4; e++ {
		eLevel[e] = s.topo.edgeLevel(NewEdgeID(p.Corners[e], p.Corners[(e+1)%4]))
	}

	// edgePoint maps a boundary grid point at parameter num/N along patch
	// edge e to its canonical EdgePoint ID.
	edgePoint := func(e, num int) PointID {
		a, b := p.Corners[e], p.Corners[(e+1)%4]
		id := NewEdgeID(a, b)
		den := 1 << eLevel[e]
		n := num << (eLevel[e] - L)
		if a > b {
			n = den - n
		}
		return PointID{Kind: EdgePoint, Edge: id, Num: n, Den: den}
	}

	// gridID maps grid coordinate (i, j) to its point ID.
	gridID := func(i, j int) PointID {
		switch {
		case i == 0 && j == 0:
			return PointID{Kind: CornerPoint, Vert: p.Corners[0]}
		case i == N && j == 0:
			return PointID{Kind: CornerPoint, Vert: p.Corners[1]}
		case i == N && j == N:
			return PointID{Kind: CornerPoint, Vert: p.Corners[2]}
		case i == 0 && j == N:
			return PointID{Kind: CornerPoint, Vert: p.Corners[3]}
		case j == 0:
			return edgePoint(0, i)
		case i == N:
			return edgePoint(1, j)
		case j == N:
			return edgePoint(2, N-i)
		case i == 0:
			return edgePoint(3, N-j)
		default:
			return PointID{Kind: InteriorPoint, Patch: p.ID, I: i, J: j}
		}
	}

	// midID returns the midpoint of cell k along patch boundary edge e,
	// at parameter (2k+1)/(2N) measured along the patch edge direction.
	midID := func(e, k int) PointID {
		num := 2*k + 1
		if e == 2 || e == 3 {
			num = 2*(N-k) - 1
		}
		a, b := p.Corners[e], p.Corners[(e+1)%4]
		id := NewEdgeID(a, b)
		den := 2 * N
		if a > b {
			num = den - num
		}
		return PointID{Kind: EdgePoint, Edge: id, Num: num, Den: den}
	}

	add := func(id PointID) {
		if _, ok := pm.Points[id]; !ok {
			pm.Points[id] = s.position(id)
		}
	}

	for j := 0; j < N; j++ {
		for i := 0; i < N; i++ {
			// Cell corners in CCW order, with edge midpoints inserted
			// along boundary edges whose neighbour is one level finer.
			// isMid[k] marks polygon vertices that are inserted
			// midpoints rather than original cell corners.
			poly := make([]PointID, 0, 8)
			isMid := make([]bool, 0, 8)
			push := func(id PointID, mid bool) {
				poly = append(poly, id)
				isMid = append(isMid, mid)
			}
			push(gridID(i, j), false)
			if j == 0 && eLevel[0] > L {
				push(midID(0, i), true)
			}
			push(gridID(i+1, j), false)
			if i == N-1 && eLevel[1] > L {
				push(midID(1, j), true)
			}
			push(gridID(i+1, j+1), false)
			if j == N-1 && eLevel[2] > L {
				push(midID(2, i), true)
			}
			push(gridID(i, j+1), false)
			if i == 0 && eLevel[3] > L {
				push(midID(3, j), true)
			}
			triangulate := func(apex int) {
				m := len(poly)
				for k := 1; k+1 < m; k++ {
					pm.Triangles = append(pm.Triangles, Triangle{
						poly[apex], poly[(apex+k)%m], poly[(apex+k+1)%m],
					})
				}
			}
			// Fan from an original corner that is not incident to any
			// subdivided cell edge; this avoids degenerate collinear
			// triangles. If no such corner exists (only possible for a
			// single-cell patch), fan from the cell center instead.
			apex := -1
			for k := range poly {
				if isMid[k] {
					continue
				}
				prev := (k + len(poly) - 1) % len(poly)
				next := (k + 1) % len(poly)
				if !isMid[prev] && !isMid[next] {
					apex = k
					break
				}
			}
			if apex >= 0 {
				triangulate(apex)
			} else {
				center := PointID{Kind: CenterPoint, Patch: p.ID, I: i, J: j}
				poly = append(poly, center)
				isMid = append(isMid, false)
				m := len(poly) - 1
				for k := 0; k < m; k++ {
					pm.Triangles = append(pm.Triangles, Triangle{
						center, poly[k], poly[(k+1)%m],
					})
				}
			}
			for _, id := range poly {
				add(id)
			}
		}
	}
	return pm
}

// position evaluates a point. Edge points are interpolated from the
// canonical edge endpoints (Edge.Lo towards Edge.Hi), so both adjacent
// patches produce bitwise identical positions.
func (s *Stitcher) position(id PointID) Vec3 {
	switch id.Kind {
	case CornerPoint:
		return s.cornerPos(id.Vert)
	case EdgePoint:
		a := s.cornerPos(id.Edge.Lo)
		b := s.cornerPos(id.Edge.Hi)
		t := float64(id.Num) / float64(id.Den)
		return Vec3{
			a[0] + (b[0]-a[0])*t,
			a[1] + (b[1]-a[1])*t,
			a[2] + (b[2]-a[2])*t,
		}
	case InteriorPoint:
		p := s.topo.patches[s.topo.patchIdx[id.Patch]]
		n := float64(int(1) << p.Level)
		return s.eval(id.Patch, float64(id.I)/n, float64(id.J)/n)
	default: // CenterPoint
		p := s.topo.patches[s.topo.patchIdx[id.Patch]]
		n := float64(int(1) << p.Level)
		return s.eval(id.Patch, (float64(id.I)+0.5)/n, (float64(id.J)+0.5)/n)
	}
}
