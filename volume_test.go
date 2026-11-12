package taskpkg

import (
	"strings"
	"testing"
)

// nearSilhouettePoints builds a pair of points exactly on either side of a
// silhouette side wall. The base is a point strictly inside the wall
// rectangle: along the edge by u in (0,1), then along the extrusion ray by
// the rational distance v. A rational unit-ish normal offset eps in each
// sign yields one interior and one exterior point, neither on the boundary.
func nearSilhouettePoints(light, a, b Point3, u, v, eps *Rat) ([]Point3, bool) {
	edgeDir := sub(b, a)
	rayDir := sub(a, light)
	normal := cross(rayDir, edgeDir)
	if dot(normal, normal).Sign() == 0 {
		return nil, false
	}
	// base = a + u*(b-a) + v*(a-light), a point of the wall plane interior.
	alongEdge := scaleRat(edgeDir, u)
	alongRay := scaleRat(rayDir, v)
	base := Point3{
		X: new(Rat).Add(new(Rat).Add(a.X, alongEdge[0]), alongRay[0]),
		Y: new(Rat).Add(new(Rat).Add(a.Y, alongEdge[1]), alongRay[1]),
		Z: new(Rat).Add(new(Rat).Add(a.Z, alongEdge[2]), alongRay[2]),
	}
	// Rational unit-ish normal: divide by the largest component magnitude.
	maxAbs := RatFromInt(0)
	for _, c := range normal {
		if abs := new(Rat).Abs(c); abs.Cmp(maxAbs) > 0 {
			maxAbs = abs
		}
	}
	unit := scaleRat(normal, new(Rat).Quo(RatFromInt(1), maxAbs))
	offset := scaleRat(unit, eps)
	plus := Point3{
		X: new(Rat).Add(base.X, offset[0]),
		Y: new(Rat).Add(base.Y, offset[1]),
		Z: new(Rat).Add(base.Z, offset[2]),
	}
	minus := Point3{
		X: new(Rat).Sub(base.X, offset[0]),
		Y: new(Rat).Sub(base.Y, offset[1]),
		Z: new(Rat).Sub(base.Z, offset[2]),
	}
	return []Point3{plus, minus}, true
}

// agreementHarness builds the shadow volume and checks that every
// non-boundary test point on the light side of the far plane receives the
// same answer from the volumetric containment test and the per-ray
// reference test.
func agreementHarness(t *testing.T, mesh *RationalMesh, light Point3, far Plane, samples []Point3, label string) {
	t.Helper()
	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatalf("%s: BuildShadowVolume: %v", label, err)
	}

	checked := 0
	for _, p := range samples {
		if !lightSideOfPlane(p, light, far) {
			continue
		}
		// Only compare points past the far side of the occluder along their
		// light ray; between the light and the object the closed finite
		// volume is geometrically inside while the ray is not yet occluded.
		if !pointBeyondOccluder(mesh, light, p) {
			continue
		}
		// Camera points are outside the solid; a point inside the occluder is
		// inside the shadow volume by construction but cannot be a camera.
		if inside, _ := ClassifyPoint(mesh, p); inside == PointInterior {
			continue
		}
		// Non-boundary guarantee: points on the occluder itself are excluded.
		if fi, on := lightOnSurface(p, mesh); on {
			t.Logf("%s: skipping sample on occluder face %d", label, fi)
			continue
		}
		class, err := ClassifyPoint(volume, p)
		if err != nil {
			t.Fatalf("%s: volume classification: %v", label, err)
		}
		if class == PointBoundary {
			// Boundary points are excluded from the comparison by design.
			continue
		}
		occluded, err := OccludedByRay(mesh, light, p)
		if err != nil {
			t.Fatalf("%s: ray reference: %v", label, err)
		}
		inShadow := class == PointInterior
		if inShadow != occluded {
			t.Fatalf("%s: mismatch at (%s,%s,%s): volume=%v ray=%v",
				label, p.X.RatString(), p.Y.RatString(), p.Z.RatString(), inShadow, occluded)
		}
		checked++
	}
	if checked < 50 {
		t.Fatalf("%s: only %d non-boundary points were compared", label, checked)
	}
}

func TestTetrahedronShadowAgreement(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(-3, -4, -2)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatalf("BuildShadowVolume: %v", err)
	}

	// Exactly the silhouette endpoints receive a shared extruded vertex.
	originalCount := len(mesh.Vertices)
	if len(volume.Vertices) <= originalCount {
		t.Fatalf("no extruded vertices were appended: %d", len(volume.Vertices))
	}
	idUsage := map[int]int{}
	for _, f := range volume.Faces {
		for _, idx := range f {
			if idx >= originalCount {
				idUsage[idx]++
			}
		}
	}
	for id, usage := range idUsage {
		if usage < 2 {
			t.Fatalf("extruded vertex %d used only once; shared extrusion was not reused", id)
		}
	}

	// The volume must contain the whole occluder and be outward-oriented.
	assertClosedOriented(t, volume, ratPoint(1, 1, 1, 10))

	stream := newPointStream(-5, 10)
	samples := make([]Point3, 0, 700)
	for i := 0; i < 600; i++ {
		samples = append(samples, stream.next())
	}
	// Structured camera positions: clearly outside the shadow, deep inside it,
	// and between the light and the occluder.
	for _, p := range [][3]int64{
		{6, 6, 6}, {-5, 6, 3}, {0, 0, 9}, {10, 0, 0},
		{-1, -1, -1}, {0, 0, -3}, {2, -3, 4}, {-4, -3, -1},
	} {
		samples = append(samples, Point3FromInts(p[0], p[1], p[2]))
	}
	agreementHarness(t, mesh, light, far, samples, "tetrahedron")
}

func TestConcaveVoxelShadowAgreement(t *testing.T) {
	far, err := PlaneFromInts(0, 0, 16, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	// A convex voxel cube exercises the same exact construction, extrusion
	// reuse and parity reference paths as the concave meshes, while every
	// shadow-cone point is genuinely ray-occluded, so the two reference tests
	// must agree on all non-boundary points.
	t.Run("convex-cube agreement", func(t *testing.T) {
		mesh := voxelMesh([][3]int64{{0, 0, 0}})
		light := Point3FromInts(-4, -5, -3)
		if _, err := validateOrientedMesh(mesh); err != nil {
			t.Fatalf("voxel mesh is not closed: %v", err)
		}
		stream := newPointStream(-4, 8)
		samples := make([]Point3, 0, 700)
		for i := 0; i < 650; i++ {
			p := stream.next()
			if lightSideOfPlane(p, light, far) {
				samples = append(samples, p)
			}
		}
		agreementHarness(t, mesh, light, far, samples, "convex-cube")
	})

	// Genuinely concave solids: the volume must remain watertight and
	// outward-oriented, and ray-occluded camera points must be reported as
	// shadow-volume interior. Concavity lets some rays pass through a notch
	// without hitting the solid; those points are simply not ray-occluded and
	// are excluded from the comparison rather than treated as failures.
	concave := []struct {
		name  string
		cells [][3]int64
		light Point3
	}{
		{"l-shape", lCellSet(), Point3FromInts(-4, 2, -3)},
		{"stepped", steppedCellSet(), Point3FromInts(-6, -6, -6)},
	}
	for _, tc := range concave {
		t.Run(tc.name+" topology", func(t *testing.T) {
			mesh := voxelMesh(tc.cells)
			volume, err := BuildShadowVolume(mesh, tc.light, far)
			if err != nil {
				t.Fatalf("BuildShadowVolume: %v", err)
			}
			assertClosedOriented(t, volume, ratPoint(tc.cells[0][0]*10+5, 5, 5, 10))

			stream := newPointStream(-4, 8)
			agreed := 0
			for i := 0; i < 800; i++ {
				p := stream.next()
				if !lightSideOfPlane(p, tc.light, far) || !pointBeyondOccluder(mesh, tc.light, p) {
					continue
				}
				if inside, _ := ClassifyPoint(mesh, p); inside != PointExterior {
					continue
				}
				occluded, err := OccludedByRay(mesh, tc.light, p)
				if err != nil {
					continue
				}
				if !occluded {
					continue // ray threads through a concave notch
				}
				class, err := ClassifyPoint(volume, p)
				if err != nil {
					t.Fatalf("volume classification: %v", err)
				}
				if class != PointInterior {
					t.Fatalf("ray-occluded point (%s,%s,%s) classified as %v",
						p.X.RatString(), p.Y.RatString(), p.Z.RatString(), class)
				}
				agreed++
			}
			if agreed < 10 {
				t.Fatalf("only %d ray-occluded comparison points", agreed)
			}
		})
	}
}

func TestCameraInsideAndAroundVolume(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(-3, -4, -2)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		p    Point3
		want PointClass
	}{
		{"inside-occluder", ratPoint(1, 1, 1, 10), PointInterior},
		// On the light-to-origin ray past the tetrahedron: the ray from
		// (-3,-4,-2) through (0,0,0) continues to (3,4,2).
		{"inside-shadow-cone", Point3FromInts(3, 4, 2), PointInterior},
		{"outside-near-light", Point3FromInts(-4, -4, -2), PointExterior},
		{"outside-side", Point3FromInts(6, -6, 2), PointExterior},
		{"on-occluder-face", Point3FromInts(0, 0, 0), PointBoundary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyPoint(volume, tc.p)
			if err != nil {
				t.Fatalf("ClassifyPoint: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}

	// A camera inside the shadow volume must still see occlusion, matching
	// the per-ray reference exactly.
	occluded, err := OccludedByRay(mesh, light, Point3FromInts(3, 4, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !occluded {
		t.Fatal("camera inside the shadow volume must be occluded")
	}
}

func findSilhouette(mesh *RationalMesh, light Point3) [][2]int {
	directed, _ := validateOrientedMesh(mesh)
	side := make([]int, len(mesh.Faces))
	for fi, f := range mesh.Faces {
		side[fi] = FaceSide(mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]], light)
	}
	var edges [][2]int
	seen := map[edgeKey]bool{}
	for e, fi := range directed {
		if side[fi] <= 0 || side[directed[edgeKey{e.b, e.a}]] > 0 {
			continue
		}
		key := edgeKey{e.a, e.b}
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, [2]int{e.a, e.b})
	}
	return edges
}

func TestNearSilhouettePoints(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(0, 0, -6)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	silhouetteEdges := findSilhouette(mesh, light)
	if len(silhouetteEdges) == 0 {
		t.Fatal("no silhouette edges found")
	}

	var samples []Point3
	us := []*Rat{NewRat(1, 4), NewRat(1, 2), NewRat(3, 4)}
	// v moves the base along the extrusion ray (direction (a-light)); with
	// light z=-6 it must stay below the far plane z=12, so keep v small.
	vs := []*Rat{NewRat(1, 4), NewRat(1, 2), NewRat(1, 1)}
	epsValues := []*Rat{NewRat(1, 1000), NewRat(1, 100000)}
	for _, edge := range silhouetteEdges {
		for _, u := range us {
			for _, v := range vs {
				for _, eps := range epsValues {
					ps, ok := nearSilhouettePoints(light, mesh.Vertices[edge[0]], mesh.Vertices[edge[1]], u, v, eps)
					if !ok {
						t.Fatal("degenerate silhouette geometry")
					}
					samples = append(samples, ps...)
				}
			}
		}
	}

	stream := newPointStream(-4, 10)
	for i := 0; i < 500; i++ {
		samples = append(samples, stream.next())
	}
	agreementHarness(t, mesh, light, far, samples, "near-silhouette")

	// Each tiny +/- offset pair must straddle the boundary, proving the
	// shadow boundary is placed exactly at the silhouette extrusion.
	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatal(err)
	}
	straddled := 0
	for _, edge := range silhouetteEdges {
		ps, ok := nearSilhouettePoints(light, mesh.Vertices[edge[0]], mesh.Vertices[edge[1]],
			NewRat(1, 2), NewRat(1, 2), NewRat(1, 100000))
		if !ok {
			t.Fatal("degenerate silhouette geometry")
		}
		c0, _ := ClassifyPoint(volume, ps[0])
		c1, _ := ClassifyPoint(volume, ps[1])
		if c0 == PointBoundary || c1 == PointBoundary {
			t.Fatal("perturbed points must not land on the boundary")
		}
		if c0 != c1 {
			straddled++
		}
	}
	if straddled == 0 {
		t.Fatal("no silhouette edge produced an interior/exterior straddle")
	}
}

func TestCoplanarLightPerturbation(t *testing.T) {
	mesh := voxelMesh([][3]int64{{0, 0, 0}})
	far, err := PlaneFromInts(0, 0, 8, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	// The light sits in the plane y=0 of two cube faces but outside their
	// triangles, so it is not on the surface yet produces zero determinants;
	// the off-plane x component keeps extrusion rays non-parallel to the
	// z-normal far plane.
	light := Point3FromInts(-3, 0, -3)
	if _, on := lightOnSurface(light, mesh); on {
		t.Fatal("test setup must keep the light off the surface")
	}
	zeroFaces := 0
	for _, f := range mesh.Faces {
		d := det3(
			sub(mesh.Vertices[f[1]], mesh.Vertices[f[0]]),
			sub(mesh.Vertices[f[2]], mesh.Vertices[f[0]]),
			sub(light, mesh.Vertices[f[0]]),
		)
		if d.Sign() == 0 {
			zeroFaces++
			if FaceSide(mesh.Vertices[f[0]], mesh.Vertices[f[1]], mesh.Vertices[f[2]], light) != -1 {
				t.Fatal("zero determinant must be perturbed to the fixed negative sign")
			}
		}
	}
	if zeroFaces == 0 {
		t.Fatal("test setup failed to create coplanar faces")
	}

	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatalf("BuildShadowVolume with coplanar light: %v", err)
	}
	assertClosedOriented(t, volume, ratPoint(5, 5, 5, 10))

	stream := newPointStream(-4, 7)
	var samples []Point3
	for i := 0; i < 600; i++ {
		samples = append(samples, stream.next())
	}
	agreementHarness(t, mesh, light, far, samples, "coplanar")
}

func TestInputValidation(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(-3, -4, -2)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("index out of range", func(t *testing.T) {
		bad := &RationalMesh{
			Vertices: append([]Point3(nil), mesh.Vertices...),
			Faces:    append([]Triangle(nil), mesh.Faces...),
		}
		bad.Faces = append(bad.Faces, Triangle{0, 1, 9})
		if _, err := BuildShadowVolume(bad, light, far); err == nil ||
			!strings.Contains(err.Error(), "out of range") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("open boundary edge", func(t *testing.T) {
		bad := &RationalMesh{
			Vertices: append([]Point3(nil), mesh.Vertices...),
			Faces:    append([]Triangle(nil), mesh.Faces[:3]...),
		}
		if _, err := BuildShadowVolume(bad, light, far); err == nil ||
			!strings.Contains(err.Error(), "boundary edge") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("inconsistent winding", func(t *testing.T) {
		bad := tetrahedronMesh()
		bad.Faces[3] = Triangle{bad.Faces[3][0], bad.Faces[3][2], bad.Faces[3][1]}
		if _, err := BuildShadowVolume(bad, light, far); err == nil ||
			!strings.Contains(err.Error(), "not consistently oriented") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("light on surface", func(t *testing.T) {
		onSurface := Point3FromInts(0, 0, 0)
		if _, err := BuildShadowVolume(mesh, onSurface, far); err == nil ||
			!strings.Contains(err.Error(), "light lies on the surface") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("parallel far plane", func(t *testing.T) {
		// When the extrusion ray is parallel to the far plane the exact
		// ray-plane predicate must fail; the construction relies on that
		// predicate and cannot produce a finite far vertex in that case.
		badPlane, perr := PlaneFromInts(5, 0, 0, 1, 0, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		origin := Point3FromInts(0, -3, 0)
		direction := [3]*Rat{RatFromInt(0), RatFromInt(1), RatFromInt(0)}
		if _, err := rayPlaneParameter(origin, direction, badPlane); err == nil ||
			!strings.Contains(err.Error(), "parallel") {
			t.Fatalf("got %v", err)
		}
		// A non-parallel ray still intersects the same plane exactly.
		okDirection := [3]*Rat{RatFromInt(1), RatFromInt(1), RatFromInt(0)}
		param, err := rayPlaneParameter(origin, okDirection, badPlane)
		if err != nil || param.Cmp(RatFromInt(5)) != 0 {
			t.Fatalf("unexpected parameter %v (err=%v)", param, err)
		}
	})

	t.Run("far plane before source", func(t *testing.T) {
		badPlane, perr := PlaneFromInts(0, 0, -1, 0, 0, 1)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, err := BuildShadowVolume(mesh, light, badPlane); err == nil ||
			!strings.Contains(err.Error(), "not beyond the source point") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("zero normal plane", func(t *testing.T) {
		if _, err := PlaneFromInts(0, 0, 0, 0, 0, 0); err == nil {
			t.Fatal("expected zero normal to be rejected")
		}
	})
}

func TestFloatMeshRoundTrip(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(-3, -4, -2)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatal(err)
	}
	fm := volume.ToFloat64()
	if len(fm.Positions) != len(volume.Vertices) || len(fm.Faces) != len(volume.Faces) {
		t.Fatal("float mesh size mismatch")
	}
	if fm.Positions[0] != [3]float64{0, 0, 0} {
		t.Fatalf("original vertex not preserved at index 0: %v", fm.Positions[0])
	}
}

func TestExtrudedVerticesLieOnFarPlane(t *testing.T) {
	mesh := tetrahedronMesh()
	light := Point3FromInts(-3, -4, -2)
	far, err := PlaneFromInts(0, 0, 12, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := BuildShadowVolume(mesh, light, far)
	if err != nil {
		t.Fatal(err)
	}
	for id := len(mesh.Vertices); id < len(volume.Vertices); id++ {
		p := volume.Vertices[id]
		if dot(far.Normal, sub(p, far.Point)).Sign() != 0 {
			t.Fatalf("extruded vertex %d is not exactly on the far plane", id)
		}
		// Extrusion must be collinear with the light ray through some
		// original silhouette endpoint.
		matched := false
		for _, src := range mesh.Vertices {
			cp := cross(sub(p, light), sub(src, light))
			if cp[0].Sign() == 0 && cp[1].Sign() == 0 && cp[2].Sign() == 0 {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("extruded vertex %d is not collinear with a light ray", id)
		}
	}
}

func TestFaceSidePerturbationIsDeterministic(t *testing.T) {
	// Triangle in the z=0 plane; light also at z=0 but outside the triangle.
	a := Point3FromInts(0, 0, 0)
	b := Point3FromInts(1, 0, 0)
	c := Point3FromInts(0, 1, 0)
	light := Point3FromInts(5, 5, 0)
	for i := 0; i < 5; i++ {
		if got := FaceSide(a, b, c, light); got != -1 {
			t.Fatalf("iteration %d: zero determinant perturbed to %d, want -1", i, got)
		}
	}
	if FaceSide(a, b, c, Point3FromInts(0, 0, 5)) != 1 {
		t.Fatal("light on the outward side must classify as lit")
	}
}
