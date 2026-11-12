package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"
)

func unitCube() Mesh {
	verts := []Vec3{
		{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0},
		{0, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 1, 1},
	}
	tris := []Triangle{
		{0, 2, 1}, {0, 3, 2}, // z=0
		{4, 5, 6}, {4, 6, 7}, // z=1
		{0, 1, 5}, {0, 5, 4}, // y=0
		{1, 2, 6}, {1, 6, 5}, // x=1
		{3, 6, 2}, {3, 7, 6}, // y=1
		{0, 4, 7}, {0, 7, 3}, // x=0
	}
	return Mesh{Vertices: verts, Triangles: tris}
}

func tetraFaces(a, b, c, d int) []Triangle {
	tris := []Triangle{{a, c, b}, {a, b, d}, {a, d, c}, {b, c, d}}
	m := Mesh{
		Vertices:  []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1}},
		Triangles: tris,
	}
	v, _ := SignedVolume(m)
	if v < 0 {
		for i := range tris {
			tris[i][1], tris[i][2] = tris[i][2], tris[i][1]
		}
	}
	return tris
}

func TestCubeHalfCut(t *testing.T) {
	m := unitCube()
	if err := m.Validate(); err != nil {
		t.Fatalf("cube should be valid: %v", err)
	}
	orig, err := SignedVolume(m)
	if err != nil || math.Abs(orig-1) > 1e-12 {
		t.Fatalf("cube volume = %v, %v", orig, err)
	}
	plane, err := PlaneThrough(Vec3{0, 0, 1}, Vec3{0, 0, 0.5})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-0.5) > 1e-9 {
		t.Errorf("cut volume = %.12g, want 0.5", vol)
	}
	// The cube's side quads are triangulated, so both the vertical edges and
	// the face diagonals cross the plane. Each original edge must still yield
	// exactly one shared point.
	if len(res.Intersections) != 8 {
		t.Errorf("got %d edge intersections, want 8", len(res.Intersections))
	}
	seenIDs := map[int]bool{}
	usedVertex := make(map[int]int)
	for _, tri := range res.Triangles {
		for _, id := range tri {
			usedVertex[id]++
		}
	}
	for _, in := range res.Intersections {
		if seenIDs[in.EdgeID] {
			t.Errorf("edge id %d created more than one intersection record", in.EdgeID)
		}
		seenIDs[in.EdgeID] = true
		if in.T <= 0 || in.T >= 1 {
			t.Errorf("intersection T=%v outside (0,1)", in.T)
		}
		if math.Abs(plane.Eval(in.Point)) > 1e-12 {
			t.Errorf("intersection off plane: %v", plane.Eval(in.Point))
		}
		if usedVertex[in.Vertex] < 2 {
			t.Errorf("intersection vertex %d is not shared by adjacent triangles", in.Vertex)
		}
	}
	if len(res.Loops) != 1 || res.Loops[0].Area <= 0 {
		t.Errorf("loops = %+v, want one positive outer loop", res.Loops)
	}
	checkCapNormals(t, res, plane)
}

func TestCubeVertexOnPlane(t *testing.T) {
	m := unitCube()
	plane, err := PlaneThrough(Vec3{1, 1, 1}, Vec3{1, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-5.0/6.0) > 1e-9 {
		t.Errorf("cut volume = %.12g, want 5/6", vol)
	}
	if len(res.Intersections) != 3 {
		t.Errorf("got %d new intersections, want 3 face-diagonal crossings", len(res.Intersections))
	}
	if len(res.Loops) != 1 {
		t.Errorf("cap loops = %+v, want one loop", res.Loops)
	}
	for _, want := range []Vec3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
		if !loopHasPosition(res, want) {
			t.Errorf("cap loop does not retain original on-plane vertex %v", want)
		}
	}
	if len(res.CapTriangles) == 0 {
		t.Errorf("no cap triangles generated")
	}
	checkCapNormals(t, res, plane)
}

func TestCubeFourVerticesOnPlane(t *testing.T) {
	m := unitCube()
	plane, err := PlaneThrough(Vec3{1, 1, 0}, Vec3{1, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-0.5) > 1e-9 {
		t.Errorf("cut volume = %.12g, want 0.5", vol)
	}
	if len(res.Loops) != 1 {
		t.Errorf("cap loops = %+v, want one loop", res.Loops)
	}
	for z := 0.0; z <= 1; z++ {
		for _, want := range []Vec3{{1, 0, z}, {0, 1, z}} {
			if !loopHasPosition(res, want) {
				t.Errorf("cap loop does not retain original on-plane vertex %v", want)
			}
		}
	}
	checkCapNormals(t, res, plane)
}

func TestCubeObliquePlane(t *testing.T) {
	m := unitCube()
	plane, err := PlaneThrough(Vec3{1, 1, 1}, Vec3{0.5, 0.5, 0.5})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	// The unit cube is centrally symmetric about (0.5,0.5,0.5), and the
	// complement of x+y+z >= 1.5 is its reflection x+y+z <= 1.5.
	if math.Abs(vol-0.5) > 1e-9 {
		t.Errorf("oblique cut volume = %.12g, want 0.5", vol)
	}
	if len(res.Loops) != 1 || res.Loops[0].Area <= 0 {
		t.Errorf("oblique cap loops = %+v, want one positive loop", res.Loops)
	}
	checkCapNormals(t, res, plane)
}

func TestCubeCornerTetraCut(t *testing.T) {
	m := unitCube()
	plane, err := NewPlane(1, 1, 1, -0.5)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	// The removed corner is a tetrahedron with edge length 0.5.
	want := 1 - 0.5*0.5*0.5/6
	if math.Abs(vol-want) > 1e-9 {
		t.Errorf("corner cut volume = %.12g, want %v", vol, want)
	}
	if len(res.Intersections) != 6 {
		t.Errorf("corner cut intersections = %d, want 6 including face diagonals", len(res.Intersections))
	}
	if len(res.Loops) != 1 || len(res.CapTriangles) == 0 {
		t.Errorf("corner cap loops = %+v, want one non-empty cap", res.Loops)
	}
	checkCapNormals(t, res, plane)
}

func TestCubeOppositeCornerTetra(t *testing.T) {
	m := unitCube()
	plane, err := NewPlane(1, 1, 1, -2.5)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-0.5*0.5*0.5/6) > 1e-9 {
		t.Errorf("opposite corner volume = %.12g, want 1/48", vol)
	}
	checkCapNormals(t, res, plane)
}

func TestCubeNegativeAxisPlane(t *testing.T) {
	m := unitCube()
	plane, err := NewPlane(-1, 0, 0, 0.5) // keep x <= 0.5
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-0.5) > 1e-9 {
		t.Errorf("negative-axis cut volume = %.12g, want 0.5", vol)
	}
	checkCapNormals(t, res, plane)
}

func TestExactRationalPlane(t *testing.T) {
	m := unitCube()
	p, err := NewExactPlane(
		big.NewRat(1, 1), big.NewRat(0, 1), big.NewRat(0, 1), big.NewRat(-1, 3),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CutExact(m, p)
	if err != nil {
		t.Fatalf("exact cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-2.0/3.0) > 1e-9 {
		t.Errorf("exact rational cut volume = %.12g, want 2/3", vol)
	}
	for _, in := range res.Intersections {
		if math.Abs(in.Point.X-1.0/3.0) > 1e-12 {
			t.Errorf("exact intersection X = %.12g, want 1/3", in.Point.X)
		}
	}
}

func TestCoplanarFaceNotDuplicated(t *testing.T) {
	m := unitCube()
	plane, err := PlaneThrough(Vec3{0, 0, 1}, Vec3{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-1) > 1e-12 {
		t.Errorf("volume = %.12g, want 1", vol)
	}
	if len(res.CapTriangles) != 0 {
		t.Errorf("generated %d duplicate cap triangles on an existing coplanar face", len(res.CapTriangles))
	}
	if res.CoplanarRetained != 2 {
		t.Errorf("retained %d coplanar triangles, want 2", res.CoplanarRetained)
	}
}

func TestCoplanarBackFaceDropped(t *testing.T) {
	m := unitCube()
	// Keeping z <= 0 from a solid that occupies z >= 0 leaves only the
	// zero-volume bottom face. Its original normal points into the retained
	// side, so it is a back face of the retained half-space and is dropped.
	plane, err := NewPlane(0, 0, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	if len(res.Mesh.Triangles) != 0 {
		t.Errorf("got %d triangles, want empty retained region", len(res.Mesh.Triangles))
	}
	if res.CoplanarDropped != 2 {
		t.Errorf("dropped %d coplanar triangles, want 2", res.CoplanarDropped)
	}
}

func TestPlaneOutsideMesh(t *testing.T) {
	m := unitCube()
	plane, err := NewPlane(0, 0, 1, -2) // keep z >= 2
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	if len(res.Mesh.Triangles) != 0 || len(res.CapTriangles) != 0 {
		t.Errorf("outside plane produced %d triangles and %d caps", len(res.Mesh.Triangles), len(res.CapTriangles))
	}
}

func TestTwoHolePrism(t *testing.T) {
	m := twoHolePrism()
	if err := m.Validate(); err != nil {
		t.Fatalf("prism should be a closed manifold: %v", err)
	}
	orig, err := SignedVolume(m)
	if err != nil || math.Abs(orig-26) > 1e-9 {
		t.Fatalf("prism volume = %v, %v", orig, err)
	}
	plane, err := PlaneThrough(Vec3{0, 0, 1}, Vec3{0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-13) > 1e-9 {
		t.Errorf("cut volume = %.12g, want 13", vol)
	}
	if len(res.Loops) != 3 {
		t.Errorf("got %d cap loops, want outer + 2 holes", len(res.Loops))
	}
	var outers, holes int
	for _, l := range res.Loops {
		switch {
		case l.Depth == 0 && l.Area > 0:
			outers++
		case l.Depth == 1 && l.Area < 0:
			holes++
		}
	}
	if outers != 1 || holes != 2 {
		t.Errorf("loops = %+v, want 1 outer and 2 holes", res.Loops)
	}
	if len(res.CapTriangles) == 0 {
		t.Error("no cap triangles generated")
	}
	checkCapNormals(t, res, plane)
}

func TestDisjointSolids(t *testing.T) {
	a := unitCube()
	b := unitCube()
	m := Mesh{}
	m.Vertices = append(m.Vertices, a.Vertices...)
	offset := len(a.Vertices)
	for _, v := range b.Vertices {
		m.Vertices = append(m.Vertices, Vec3{v.X + 2, v.Y, v.Z})
	}
	m.Triangles = append(m.Triangles, a.Triangles...)
	for _, tri := range b.Triangles {
		m.Triangles = append(m.Triangles, Triangle{tri[0] + offset, tri[1] + offset, tri[2] + offset})
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("two cubes should be valid: %v", err)
	}
	plane, err := NewPlane(0, 0, 1, -0.5)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-1) > 1e-9 {
		t.Errorf("disjoint cut volume = %.12g, want 1", vol)
	}
	if len(res.Loops) != 2 {
		t.Errorf("got %d cap loops, want 2 disjoint outer loops", len(res.Loops))
	}
	checkCapNormals(t, res, plane)
}

func TestConcaveOuterWithHoles(t *testing.T) {
	m := gridPrism(7, 5, map[[2]int]bool{
		{5, 2}: true, {6, 2}: true, // notch in the outer boundary
		{5, 3}: true, {6, 3}: true,
		{5, 4}: true, {6, 4}: true,
		{1, 1}: true, {3, 1}: true, // two interior holes
	})
	if err := m.Validate(); err != nil {
		t.Fatalf("concave prism should be valid: %v", err)
	}
	orig, err := SignedVolume(m)
	if err != nil || math.Abs(orig-54) > 1e-9 {
		t.Fatalf("concave prism volume = %v, %v", orig, err)
	}
	plane, err := NewPlane(0, 0, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-27) > 1e-9 {
		t.Errorf("concave cut volume = %.12g, want 27", vol)
	}
	if len(res.Loops) != 3 {
		t.Errorf("got %d loops, want concave outer + 2 holes", len(res.Loops))
	}
	checkCapNormals(t, res, plane)
}

func TestThreeHoles(t *testing.T) {
	m := gridPrism(7, 3, map[[2]int]bool{
		{1, 1}: true, {3, 1}: true, {5, 1}: true,
	})
	if err := m.Validate(); err != nil {
		t.Fatalf("three-hole prism should be valid: %v", err)
	}
	plane, err := NewPlane(0, 0, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Cut(m, plane)
	if err != nil {
		t.Fatalf("cut failed: %v", err)
	}
	assertClosed(t, res.Mesh)
	vol, err := SignedVolume(res.Mesh)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(vol-18) > 1e-9 {
		t.Errorf("three-hole cut volume = %.12g, want 18", vol)
	}
	if len(res.Loops) != 4 {
		t.Errorf("got %d loops, want outer + 3 holes", len(res.Loops))
	}
	checkCapNormals(t, res, plane)
}

func TestRejectsOpenMesh(t *testing.T) {
	m := unitCube()
	m.Triangles = m.Triangles[:len(m.Triangles)-1]
	if err := m.Validate(); !errors.Is(err, ErrNonManifold) {
		t.Fatalf("open mesh validation = %v, want ErrNonManifold", err)
	}
	plane, _ := PlaneThrough(Vec3{0, 0, 1}, Vec3{0, 0, 0.5})
	if _, err := Cut(m, plane); !errors.Is(err, ErrNonManifold) {
		t.Fatalf("Cut(open mesh) = %v, want ErrNonManifold", err)
	}
}

func TestRejectsVertexNonManifold(t *testing.T) {
	// Two closed tetrahedra sharing exactly one vertex index. Edge pairing is
	// perfect, but the vertex link has two cycles.
	verts := []Vec3{
		{0, 0, 0},
		{1, 0, 0}, {0, 1, 0}, {0, 0, 1},
		{-1, 0, 0}, {0, -1, 0}, {0, 0, -1},
	}
	tA := tetraFaces(0, 1, 2, 3)
	b := []int{0, 4, 5, 6}
	tB := tetraFaces(b[0], b[1], b[2], b[3])
	m := Mesh{Vertices: verts, Triangles: append(tA, tB...)}
	if err := m.Validate(); !errors.Is(err, ErrNonManifold) {
		t.Fatalf("pinched vertex validation = %v, want ErrNonManifold", err)
	}
}

func checkCapNormals(t *testing.T, res *Result, plane Plane) {
	t.Helper()
	for _, idx := range res.CapIndices {
		tri := res.Triangles[idx]
		a := res.Vertices[tri[0]]
		b := res.Vertices[tri[1]]
		c := res.Vertices[tri[2]]
		n := b.Sub(a).Cross(c.Sub(a))
		if n.Dot(plane.Normal()) >= -1e-12 {
			t.Errorf("cap triangle %v normal %v does not point opposite plane normal %v",
				tri, n, plane.Normal())
		}
	}
}

func assertClosed(t *testing.T, m Mesh) {
	t.Helper()
	type use struct{ fwd, rev int }
	edges := map[Edge]use{}
	for _, tri := range m.Triangles {
		for _, e := range tri.edges() {
			u, v := e[0], e[1]
			key := edgeOf(u, v)
			x := edges[key]
			if u < v {
				x.fwd++
			} else {
				x.rev++
			}
			edges[key] = x
		}
	}
	for e, u := range edges {
		if u.fwd != 1 || u.rev != 1 {
			t.Fatalf("edge (%d,%d) uses = %d forward, %d reverse", e.A, e.B, u.fwd, u.rev)
		}
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("result is not manifold: %v", err)
	}
}

func twoHolePrism() Mesh {
	return gridPrism(5, 3, map[[2]int]bool{
		{1, 1}: true, {3, 1}: true,
	})
}

func gridPrism(nx, ny int, absent map[[2]int]bool) Mesh {
	present := func(ix, iy int) bool {
		if ix < 0 || iy < 0 || ix >= nx || iy >= ny {
			return false
		}
		return !absent[[2]int{ix, iy}]
	}
	verts := []Vec3{}
	index := map[[3]int]int{}
	get := func(ix, iy, iz int) int {
		key := [3]int{ix, iy, iz}
		if id, ok := index[key]; ok {
			return id
		}
		id := len(verts)
		verts = append(verts, Vec3{float64(ix), float64(iy), float64(iz)})
		index[key] = id
		return id
	}
	var tris []Triangle
	addQuad := func(a, b, c, d int) {
		tris = append(tris, Triangle{a, b, c}, Triangle{a, c, d})
	}
	for ix := 0; ix < nx; ix++ {
		for iy := 0; iy < ny; iy++ {
			if !present(ix, iy) {
				continue
			}
			a0, b0 := get(ix, iy, 0), get(ix+1, iy, 0)
			c0, d0 := get(ix+1, iy+1, 0), get(ix, iy+1, 0)
			a1, b1 := get(ix, iy, 2), get(ix+1, iy, 2)
			c1, d1 := get(ix+1, iy+1, 2), get(ix, iy+1, 2)

			// Top (+z) and bottom (-z).
			addQuad(a1, b1, c1, d1)
			addQuad(a0, d0, c0, b0)

			if !present(ix, iy-1) {
				addQuad(a0, b0, b1, a1) // -y
			}
			if !present(ix, iy+1) {
				addQuad(d0, d1, c1, c0) // +y
			}
			if !present(ix+1, iy) {
				addQuad(b0, c0, c1, b1) // +x
			}
			if !present(ix-1, iy) {
				addQuad(a0, a1, d1, d0) // -x
			}
		}
	}
	// Compact and validate deterministically.
	return Mesh{Vertices: verts, Triangles: tris}
}

func loopHasPosition(res *Result, want Vec3) bool {
	for _, loop := range res.Loops {
		for _, id := range loop.Vertices {
			if res.Vertices[id].almostEqual(want, 1e-12) {
				return true
			}
		}
	}
	return false
}

func ExampleCut() {
	m := unitCube()
	p, _ := PlaneThrough(Vec3{0, 0, 1}, Vec3{0, 0, 0.5})
	r, _ := Cut(m, p)
	v, _ := SignedVolume(r.Mesh)
	fmt.Printf("loops=%d capTriangles=%d volume=%.1f\n", len(r.Loops), len(r.CapTriangles), v)
	// Output:
	// loops=1 capTriangles=6 volume=0.5
}
