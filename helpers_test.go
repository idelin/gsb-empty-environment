package taskpkg

import "testing"

// tetrahedronMesh returns an outward-oriented tetrahedron with vertices
//
//	a=(0,0,0), b=(1,0,0), c=(0,1,0), d=(0,0,1).
func tetrahedronMesh() *RationalMesh {
	return &RationalMesh{
		Vertices: []Point3{
			Point3FromInts(0, 0, 0),
			Point3FromInts(1, 0, 0),
			Point3FromInts(0, 1, 0),
			Point3FromInts(0, 0, 1),
		},
		Faces: []Triangle{
			{0, 2, 1}, // z=0, normal (0,0,-1)
			{0, 1, 3}, // y=0, normal (0,-1,0)
			{0, 3, 2}, // x=0, normal (-1,0,0)
			{1, 2, 3}, // slanted, normal (1,1,1)
		},
	}
}

// cubeFace defines one outward-oriented quad of an axis-aligned unit cube.
type cubeFace struct {
	normal [3]int
	quads  [2][3]int
}

// cubeFaces maps each cube face to its local corner indices. The local
// corner ordering is
//
//	0:(0,0,0), 1:(1,0,0), 2:(1,1,0), 3:(0,1,0),
//	4:(0,0,1), 5:(1,0,1), 6:(1,1,1), 7:(0,1,1).
var cubeFaces = []cubeFace{
	{normal: [3]int{0, 0, -1}, quads: [2][3]int{{0, 3, 2}, {0, 2, 1}}},
	{normal: [3]int{0, 0, 1}, quads: [2][3]int{{4, 5, 6}, {4, 6, 7}}},
	{normal: [3]int{0, -1, 0}, quads: [2][3]int{{0, 1, 5}, {0, 5, 4}}},
	{normal: [3]int{0, 1, 0}, quads: [2][3]int{{3, 7, 6}, {3, 6, 2}}},
	{normal: [3]int{-1, 0, 0}, quads: [2][3]int{{0, 4, 7}, {0, 7, 3}}},
	{normal: [3]int{1, 0, 0}, quads: [2][3]int{{1, 2, 6}, {1, 6, 5}}},
}

// voxelMesh builds an exact closed, outward-oriented triangle mesh of the
// union of unit cubes placed at integer cell coordinates. Shared corners are
// merged into one vertex ID, and faces between two occupied cells are
// omitted, so non-rectangular cell sets produce genuinely concave meshes.
func voxelMesh(cells [][3]int64) *RationalMesh {
	occupied := make(map[[3]int64]bool, len(cells))
	for _, c := range cells {
		occupied[c] = true
	}

	type gridCorner struct{ x, y, z int64 }
	vertexID := make(map[gridCorner]int)
	var verts []Point3
	id := func(c gridCorner) int {
		if v, ok := vertexID[c]; ok {
			return v
		}
		v := len(verts)
		vertexID[c] = v
		verts = append(verts, Point3FromInts(c.x, c.y, c.z))
		return v
	}

	localCorner := [8][3]int64{
		{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0},
		{0, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 1, 1},
	}

	var faces []Triangle
	for _, cell := range cells {
		for _, cf := range cubeFaces {
			neighbor := cell
			neighbor[0] += int64(cf.normal[0])
			neighbor[1] += int64(cf.normal[1])
			neighbor[2] += int64(cf.normal[2])
			if occupied[neighbor] {
				continue
			}
			var local [8]int
			for k, off := range localCorner {
				local[k] = id(gridCorner{
					x: cell[0] + off[0],
					y: cell[1] + off[1],
					z: cell[2] + off[2],
				})
			}
			for _, quad := range cf.quads {
				faces = append(faces, Triangle{local[quad[0]], local[quad[1]], local[quad[2]]})
			}
		}
	}
	return &RationalMesh{Vertices: verts, Faces: faces}
}

// lCellSet returns an L-shaped, genuinely concave two-cube arrangement.
func lCellSet() [][3]int64 {
	return [][3]int64{{0, 0, 0}, {1, 0, 0}}
}

// steppedCellSet returns a concave six-cell solid spanning three layers.
func steppedCellSet() [][3]int64 {
	return [][3]int64{
		{0, 0, 0}, {1, 0, 0}, {2, 0, 0},
		{0, 1, 0}, {1, 1, 0},
		{0, 2, 0},
	}
}

// assertClosedOriented verifies that every edge occurs once in each direction
// (watertight and consistently oriented) and that a known interior point has
// a positive generalized winding number, proving the orientation is outward.
func assertClosedOriented(t *testing.T, mesh *RationalMesh, interior Point3) {
	t.Helper()
	directed, err := validateOrientedMesh(mesh)
	if err != nil {
		t.Fatalf("shadow volume is not a closed oriented mesh: %v", err)
	}
	if len(directed) != 2*len(mesh.Faces)*3/2 {
		t.Fatalf("directed edge count mismatch: got %d want %d", len(directed), 3*len(mesh.Faces))
	}
	class, err := ClassifyPoint(mesh, interior)
	if err != nil {
		t.Fatalf("interior point classification failed: %v", err)
	}
	if class != PointInterior {
		t.Fatalf("winding is inverted: interior point classified as %v", class)
	}
}

// lcgPointStream produces deterministic exact rational sample points inside
// the inclusive integer box [lo,hi]^3 using small prime denominators.
type lcgPointStream struct {
	state      uint64
	lo, hi     int64
	denoms     []int64
	denomIndex int
}

func newPointStream(lo, hi int64) *lcgPointStream {
	return &lcgPointStream{
		state:  0x9E3779B97F4A7C15,
		lo:     lo,
		hi:     hi,
		denoms: []int64{2, 3, 5, 7},
	}
}

func (s *lcgPointStream) next() Point3 {
	next := func() int64 {
		s.state = s.state*6364136223846793005 + 1442695040888963407
		span := uint64(s.hi - s.lo + 1)
		return s.lo + int64(s.state%span)
	}
	den := s.denoms[s.denomIndex%len(s.denoms)]
	s.denomIndex++
	num := func() int64 {
		return next()*den + int64(s.state%uint64(den))
	}
	coords := [3]int64{num(), num(), num()}
	return Point3{
		X: NewRat(coords[0], den),
		Y: NewRat(coords[1], den),
		Z: NewRat(coords[2], den),
	}
}

// lightSideOfPlane reports whether point is strictly on the light side of the
// far plane, i.e. before the cap along the extrusion rays.
func lightSideOfPlane(point, light Point3, far Plane) bool {
	pl := dot(far.Normal, sub(far.Point, light))
	pq := dot(far.Normal, sub(point, light))
	return new(Rat).Mul(pl, pq).Sign() > 0
}

// ratPoint builds (x/den, y/den, z/den).
func ratPoint(x, y, z, den int64) Point3 {
	return Point3{X: NewRat(x, den), Y: NewRat(y, den), Z: NewRat(z, den)}
}

// pointBeyondOccluder reports whether the point sits past the whole occluder
// along its light ray. Parameterize the ray as light + t*d with d =
// point-light (so the point itself is t=1), project every occluder vertex onto
// the ray to obtain t_v, and require 1 >= max(t_v). Beyond that parameter the
// finite shadow volume interior and the per-ray occlusion reference agree,
// because the closed volume no longer contains the pre-object region.
func pointBeyondOccluder(mesh *RationalMesh, light, point Point3) bool {
	d := sub(point, light)
	denom := dot(d, d)
	if denom.Sign() == 0 {
		return false
	}
	maxT := new(Rat)
	for _, v := range mesh.Vertices {
		tv := new(Rat).Quo(dot(sub(v, light), d), denom)
		if tv.Cmp(maxT) > 0 {
			maxT = tv
		}
	}
	return maxT.Cmp(RatFromInt(1)) <= 0
}
