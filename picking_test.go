package taskpkg

import (
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func mustMesh(t *testing.T, vertices []Vec3, indices []uint32) *Mesh {
	t.Helper()
	mesh, err := NewMesh(vertices, indices)
	if err != nil {
		t.Fatalf("NewMesh: %v", err)
	}
	return mesh
}

func mustSceneInstance(t *testing.T, scene *Scene, id uint64, mesh *Mesh, transform Affine) {
	t.Helper()
	if err := scene.AddInstanceWithID(id, mesh, transform); err != nil {
		t.Fatalf("AddInstanceWithID(%d): %v", id, err)
	}
}

func worldTriangle(local Triangle, transform Affine) Triangle {
	return Triangle{
		A: transform.TransformPoint(local.A),
		B: transform.TransformPoint(local.B),
		C: transform.TransformPoint(local.C),
	}
}

// referenceTriangleIntersect is deliberately evaluated entirely in world
// space. It does not use a mesh BVH or the scene local-intersection path.
func referenceTriangleIntersect(tri Triangle, ray Ray) (t, u, v float64, ok bool) {
	edge1 := tri.B.Sub(tri.A)
	edge2 := tri.C.Sub(tri.A)
	pvec := ray.Direction.Cross(edge2)
	det := edge1.Dot(pvec)
	if det == 0 || !isFinite(det) {
		return 0, 0, 0, false
	}
	invDet := 1 / det
	offset := ray.Origin.Sub(tri.A)
	u = offset.Dot(pvec) * invDet
	if u < -barycentricEpsilon || u > 1+barycentricEpsilon {
		return 0, 0, 0, false
	}
	qvec := offset.Cross(edge1)
	v = ray.Direction.Dot(qvec) * invDet
	if v < -barycentricEpsilon || u+v > 1+barycentricEpsilon {
		return 0, 0, 0, false
	}
	t = edge2.Dot(qvec) * invDet
	return t, u, v, isFinite(t)
}

func referenceWorldHits(scene *Scene, ray Ray, options PickOptions) []Hit {
	options = options.normalized()
	var hits []Hit
	for _, in := range scene.instances {
		for triangleID, local := range in.mesh.triangles {
			tri := worldTriangle(local, in.transform)
			t, u, v, ok := referenceTriangleIntersect(tri, ray)
			if !ok || t < options.TMin || t > options.MaxT {
				continue
			}

			// cross(M*e1, M*e2) = det(M) * M^-T * cross(e1,e2). Undoing
			// the determinant sign recovers the inverse-transpose normal.
			normal := tri.FaceNormal().Normalize()
			if in.detSign < 0 {
				normal = normal.Neg()
			}
			front := ray.Direction.Dot(normal) < 0
			if options.CullBackfaces && !front {
				continue
			}
			if options.CullFrontfaces && front {
				continue
			}

			hits = append(hits, Hit{
				InstanceID:  in.id,
				TriangleID:  uint32(triangleID),
				T:           t,
				Point:       ray.PointAt(t),
				Normal:      normal,
				Barycentric: [3]float64{1 - u - v, u, v},
				FrontFace:   front,
			})
		}
	}
	sortHits(hits)
	return hits
}

func referenceNearest(scene *Scene, ray Ray, options PickOptions) (Hit, bool) {
	hits := referenceWorldHits(scene, ray, options)
	if len(hits) == 0 {
		return Hit{}, false
	}
	return hits[0], true
}

func unitXYTriangle() *Mesh {
	mesh, err := NewMesh([]Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})
	if err != nil {
		panic(err)
	}
	return mesh
}

func TestNonUniformScaleUsesWorldDistance(t *testing.T) {
	// The nearer plane has a huge scale (hence a tiny inverse ray direction in
	// local space), while the farther plane has a tiny scale. A picker that
	// ranks normalised local-ray distances picks instance 20 incorrectly.
	mesh := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})
	scene := NewScene()
	mustSceneInstance(t, scene, 20, mesh, Scale(0.01, 0.01, 0.01))
	mustSceneInstance(t, scene, 10, mesh, NewAffine(
		Mat3{{100, 0, 0}, {0, 100, 0}, {0, 0, 100}},
		Vec(0, 0, 0.5),
	))

	ray := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))
	got, ok := scene.Pick(ray)
	if !ok {
		t.Fatal("expected a hit")
	}
	if got.InstanceID != 10 {
		t.Fatalf("nearest instance = %d, want 10 (far-away ID 20)", got.InstanceID)
	}
	if math.Abs(got.T-0.5) > 1e-12 {
		t.Fatalf("world T = %.17g, want 0.5", got.T)
	}
	if got.Point.Sub(Vec(0, 0, 0.5)).Length() > 1e-12 {
		t.Fatalf("world point = %+v, want (0,0,0.5)", got.Point)
	}

	ref, ok := referenceNearest(scene, ray, PickOptions{})
	if !ok || ref.InstanceID != got.InstanceID || math.Abs(ref.T-got.T) > 1e-12 {
		t.Fatalf("world reference = %+v, scene = %+v", ref, got)
	}
}

func TestEqualDistanceUsesInstanceThenTriangleID(t *testing.T) {
	mesh := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})
	scene := NewScene()

	// Add the larger ID first so traversal order cannot accidentally hide a
	// failure to compare IDs.
	mustSceneInstance(t, scene, 9, mesh, IdentityAffine())
	mustSceneInstance(t, scene, 4, mesh, IdentityAffine())
	ray := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))
	got, ok := scene.Pick(ray)
	if !ok || got.InstanceID != 4 {
		t.Fatalf("equal-distance winner = %+v, want instance 4", got)
	}
	ref, ok := referenceNearest(scene, ray, PickOptions{})
	if !ok || ref.InstanceID != 4 || ref.TriangleID != got.TriangleID {
		t.Fatalf("reference equal-distance winner = %+v", ref)
	}

	// The same rule applies inside one instance: lower triangle ID wins.
	mesh2 := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, nil)
	scene2 := NewScene()
	mustSceneInstance(t, scene2, 1, mesh2, IdentityAffine())
	got, ok = scene2.Pick(ray)
	if !ok || got.TriangleID != 0 {
		t.Fatalf("equal-distance triangle = %d, want 0", got.TriangleID)
	}

	all := scene.PickAll(ray, PickOptions{})
	if len(all) != 2 || all[0].InstanceID != 4 || all[1].InstanceID != 9 {
		t.Fatalf("PickAll order = %+v", all)
	}
}

func TestMirrorNormalAndBackfaceRule(t *testing.T) {
	mesh := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})
	scene := NewScene()
	mirrorX := NewAffine(
		Mat3{{-0.5, 0, 0}, {0, 2, 0}, {0, 0, 1.3}},
		Vec(0, 0, 0),
	)
	mustSceneInstance(t, scene, 7, mesh, mirrorX)

	fromAbove := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))
	got, ok := scene.PickWithOptions(fromAbove, PickOptions{CullBackfaces: true})
	if !ok {
		t.Fatal("mirrored front face was incorrectly culled")
	}
	wantNormal := Vec(0, 0, 1)
	if got.Normal.Sub(wantNormal).Length() > 1e-12 {
		t.Fatalf("mirror-X normal = %+v, want +Z via inverse transpose", got.Normal)
	}
	if !got.FrontFace {
		t.Fatal("mirrored mesh logical front face reported as back face")
	}
	if math.Abs(got.Normal.Length()-1) > 1e-12 {
		t.Fatalf("normal is not unit length: %.17g", got.Normal.Length())
	}

	fromBelow := NewRay(Vec(0, 0, -1), Vec(0, 0, 1))
	if _, ok := scene.PickWithOptions(fromBelow, PickOptions{CullBackfaces: true}); ok {
		t.Fatal("back face under mirror should be culled")
	}
	if _, ok := scene.PickWithOptions(fromAbove, PickOptions{CullFrontfaces: true}); ok {
		t.Fatal("front face under mirror should be culled when front culling is requested")
	}
	if _, ok := scene.PickWithOptions(fromBelow, PickOptions{CullFrontfaces: true}); !ok {
		t.Fatal("mirrored back face should remain visible with front culling")
	}
	back, ok := scene.Pick(fromBelow)
	if !ok || back.FrontFace || back.Normal.Sub(wantNormal).Length() > 1e-12 {
		t.Fatalf("two-sided mirror back hit = %+v", back)
	}
}

func TestExtremelySmallInvertibleScale(t *testing.T) {
	mesh := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})

	for _, s := range []float64{1e-9, 1e-12, 1e-15} {
		s := s
		t.Run("scale="+strconv.FormatFloat(s, 'g', -1, 64), func(t *testing.T) {
			scene := NewScene()
			mustSceneInstance(t, scene, 3, mesh, Scale(s, s, s))
			ray := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))
			got, ok := scene.Pick(ray)
			if !ok {
				t.Fatalf("tiny scale %g produced no hit", s)
			}
			if math.Abs(got.T-1) > 1e-12 {
				t.Fatalf("tiny scale %g world T = %.17g, want 1", s, got.T)
			}
			if got.Normal.Sub(Vec(0, 0, 1)).Length() > 1e-12 {
				t.Fatalf("tiny scale %g normal = %+v", s, got.Normal)
			}
			ref, ok := referenceNearest(scene, ray, PickOptions{})
			if !ok || ref.InstanceID != got.InstanceID || math.Abs(ref.T-got.T) > 1e-10 {
				t.Fatalf("tiny scale reference = %+v, scene = %+v", ref, got)
			}
		})
	}
}

func TestInstanceUpdateRefitsTLASWithoutRebuildingMeshBLAS(t *testing.T) {
	mesh := mustMesh(t, []Vec3{
		Vec(-1, -1, 0),
		Vec(1, -1, 0),
		Vec(0, 1, 0),
	}, []uint32{0, 1, 2})
	scene := NewScene()
	mustSceneInstance(t, scene, 5, mesh, Translate(Vec(0, 0, 2)))
	mustSceneInstance(t, scene, 6, mesh, Translate(Vec(0, 0, 3)))

	beforeNodes := append([]bvhNode(nil), mesh.tree.nodes...)
	beforePrims := append([]uint32(nil), mesh.tree.prims...)
	beforePtr := &mesh.tree.nodes[0]

	ray := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))
	if got, ok := scene.Pick(ray); ok {
		t.Fatalf("unexpected hit before update: %+v", got)
	}

	if err := scene.UpdateInstance(5, Translate(Vec(0, 0, 0.5))); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}
	got, ok := scene.Pick(ray)
	if !ok || got.InstanceID != 5 || math.Abs(got.T-0.5) > 1e-12 {
		t.Fatalf("after update = %+v, want instance 5 at T=0.5", got)
	}
	if &mesh.tree.nodes[0] != beforePtr ||
		!reflect.DeepEqual(beforeNodes, mesh.tree.nodes) ||
		!reflect.DeepEqual(beforePrims, mesh.tree.prims) {
		t.Fatal("shared mesh BLAS changed during instance transform update")
	}
	if mesh.BVHNodeCount() != len(beforeNodes) {
		t.Fatal("shared mesh BLAS node count changed")
	}

	// The other instance still sees its own transform.
	if got, ok := scene.Pick(NewRay(Vec(0, 0, 4), Vec(0, 0, -1))); !ok || got.InstanceID != 6 {
		t.Fatalf("second shared instance after update = %+v", got)
	}
}

func TestBulkTransformUpdateRefitsInternalTLASNodes(t *testing.T) {
	mesh := cubeMesh(t)
	scene := NewScene()
	transforms := []Affine{
		NewAffine(linear(rotationY(0.1), Vec(1, 1, 1)), Vec(-3, 0, 0)),
		NewAffine(linear(rotationX(0.2), Vec(1, 1, 1)), Vec(3, 0, 0)),
		NewAffine(linear(rotationY(-0.3), Vec(1.2, 0.8, 0.7)), Vec(0, -3, 0)),
		NewAffine(linear(rotationX(0.4), Vec(0.9, 1.3, 1.1)), Vec(0, 3, 0)),
		NewAffine(linear(rotationY(0.5), Vec(0.6, 1.8, 1)), Vec(0, 0, -3)),
		NewAffine(linear(rotationX(-0.6), Vec(1.1, 0.7, 1.4)), Vec(0, 0, 3)),
	}
	for i, transform := range transforms {
		mustSceneInstance(t, scene, uint64(i+1), mesh, transform)
	}
	beforeTLASNodes := append([]bvhNode(nil), scene.tree.nodes...)
	beforeTLASPrims := append([]uint32(nil), scene.tree.prims...)
	beforeMeshPtr := &mesh.tree.nodes[0]

	updated := []Affine{
		NewAffine(linear(rotationY(-0.4), Vec(1.4, 0.9, 0.8)), Vec(0, 0, 0)),
		NewAffine(linear(rotationX(0.9), Vec(2, 0.5, 1.2)), Vec(2.5, 0.5, -1)),
		NewAffine(linear(rotationY(0.7), Vec(0.7, 1.6, 1.3)), Vec(-2, 1, 0.5)),
	}
	for i, transform := range updated {
		if err := scene.UpdateInstance(uint64(i+1), transform); err != nil {
			t.Fatalf("UpdateInstance(%d): %v", i+1, err)
		}
	}

	if !reflect.DeepEqual(beforeTLASPrims, scene.tree.prims) || !sameBVHTopology(beforeTLASNodes, scene.tree.nodes) {
		t.Fatal("transform refit changed TLAS topology or primitive order")
	}
	if &mesh.tree.nodes[0] != beforeMeshPtr {
		t.Fatal("instance transform update touched the shared mesh BLAS")
	}
	boundsChanged := false
	for i := range beforeTLASNodes {
		if beforeTLASNodes[i].bounds != scene.tree.nodes[i].bounds {
			boundsChanged = true
			break
		}
	}
	if !boundsChanged {
		t.Fatal("transform update did not refit any TLAS bounds")
	}

	rng := rand.New(rand.NewSource(7))
	for iteration := 0; iteration < 120; iteration++ {
		instanceIndex := rng.Intn(len(transforms))
		info, ok := scene.InstanceInfo(uint64(instanceIndex + 1))
		if !ok {
			t.Fatalf("missing updated instance %d", instanceIndex+1)
		}
		target := info.Transform.TransformPoint(Vec(
			rng.Float64()*2-1,
			rng.Float64()*2-1,
			rng.Float64()*2-1,
		))
		offset := Vec(rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64())
		if offset.LengthSquared() == 0 {
			offset = Vec(0, 0, 1)
		}
		ray := NewRay(target.Add(offset.Normalize().Mul(4)), offset.Normalize().Neg())
		got, gotOK := scene.Pick(ray)
		ref, refOK := referenceNearest(scene, ray, PickOptions{})
		if gotOK != refOK {
			t.Fatalf("iteration %d hit mismatch after bulk update", iteration)
		}
		if gotOK && (got.InstanceID != ref.InstanceID || got.TriangleID != ref.TriangleID) {
			t.Fatalf("iteration %d nearest mismatch after bulk update: scene=(%d,%d) reference=(%d,%d)",
				iteration, got.InstanceID, got.TriangleID, ref.InstanceID, ref.TriangleID)
		}
	}
}

func sameBVHTopology(a, b []bvhNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].left != b[i].left || a[i].right != b[i].right ||
			a[i].start != b[i].start || a[i].count != b[i].count {
			return false
		}
	}
	return true
}

func cubeMesh(t *testing.T) *Mesh {
	t.Helper()
	vertices := []Vec3{
		Vec(-1, -1, -1), Vec(1, -1, -1), Vec(1, 1, -1), Vec(-1, 1, -1),
		Vec(-1, -1, 1), Vec(1, -1, 1), Vec(1, 1, 1), Vec(-1, 1, 1),
	}
	indices := []uint32{
		0, 3, 2, 0, 2, 1,
		4, 5, 6, 4, 6, 7,
		0, 1, 5, 0, 5, 4,
		1, 2, 6, 1, 6, 5,
		2, 3, 7, 2, 7, 6,
		3, 0, 4, 3, 4, 7,
	}
	return mustMesh(t, vertices, indices)
}

func rotationY(angle float64) Mat3 {
	c, s := math.Cos(angle), math.Sin(angle)
	return Mat3{
		{c, 0, s},
		{0, 1, 0},
		{-s, 0, c},
	}
}

func rotationX(angle float64) Mat3 {
	c, s := math.Cos(angle), math.Sin(angle)
	return Mat3{
		{1, 0, 0},
		{0, c, -s},
		{0, s, c},
	}
}

func linear(rotation Mat3, scale Vec3) Mat3 {
	scaled := Mat3{
		{scale.X, 0, 0},
		{0, scale.Y, 0},
		{0, 0, scale.Z},
	}
	return rotation.Mul(scaled)
}

func TestBruteForceWorldReferenceAcrossScalesAndMirrors(t *testing.T) {
	mesh := cubeMesh(t)
	scene := NewScene()

	transforms := []Affine{
		NewAffine(linear(rotationY(0.35), Vec(2, 0.5, 1.5)), Vec(0, 0, 0)),
		NewAffine(linear(rotationX(-0.6), Vec(-0.8, 1.2, 0.6)), Vec(1, 0.4, -0.5)),
		NewAffine(linear(rotationY(0.2), Vec(1e-8, 1e-8, 1e-8)), Vec(-0.3, -0.2, 1.2)),
		NewAffine(Mat3{
			{0.8, 0.3, 0},
			{0.1, -1.2, 0.2},
			{0, 0.15, 1.4},
		}, Vec(-1.2, 1.1, -0.9)),
	}
	for i, transform := range transforms {
		mustSceneInstance(t, scene, uint64(i+1), mesh, transform)
	}

	rng := rand.New(rand.NewSource(42))
	totalHits := 0
	for iteration := 0; iteration < 300; iteration++ {
		instanceIndex := rng.Intn(len(transforms))
		localTarget := Vec(
			rng.Float64()*2-1,
			rng.Float64()*2-1,
			rng.Float64()*2-1,
		)
		target := transforms[instanceIndex].TransformPoint(localTarget)
		offset := Vec(
			rng.NormFloat64(),
			rng.NormFloat64(),
			rng.NormFloat64(),
		)
		if offset.LengthSquared() == 0 {
			offset = Vec(1, 0, 0)
		}
		offset = offset.Normalize().Mul(2 + rng.Float64()*8)
		ray := NewRay(target.Add(offset), offset.Neg())

		got, gotOK := scene.Pick(ray)
		ref, refOK := referenceNearest(scene, ray, PickOptions{})
		if gotOK != refOK {
			t.Fatalf("iteration %d hit mismatch: scene=%v reference=%v", iteration, gotOK, refOK)
		}
		if !gotOK {
			continue
		}
		totalHits++

		if got.InstanceID != ref.InstanceID || got.TriangleID != ref.TriangleID {
			t.Fatalf("iteration %d nearest ID mismatch: scene=(%d,%d) reference=(%d,%d)",
				iteration, got.InstanceID, got.TriangleID, ref.InstanceID, ref.TriangleID)
		}
		scale := math.Max(1, math.Abs(ref.T))
		if math.Abs(got.T-ref.T) > 1e-10*scale {
			t.Fatalf("iteration %d world T mismatch: scene=%.17g reference=%.17g", iteration, got.T, ref.T)
		}
		if got.Point.Sub(ref.Point).Length() > 1e-9*scale {
			t.Fatalf("iteration %d world point mismatch: scene=%+v reference=%+v", iteration, got.Point, ref.Point)
		}
		if got.Normal.Sub(ref.Normal).Length() > 1e-9 {
			t.Fatalf("iteration %d world normal mismatch: scene=%+v reference=%+v", iteration, got.Normal, ref.Normal)
		}
		if got.FrontFace != ref.FrontFace {
			t.Fatalf("iteration %d front-face mismatch", iteration)
		}
	}
	if totalHits < 200 {
		t.Fatalf("only %d/300 rays hit; reference/random test is not exercising geometry", totalHits)
	}
}

func TestCullingMatchesWorldReference(t *testing.T) {
	mesh := cubeMesh(t)
	scene := NewScene()
	mustSceneInstance(t, scene, 11, mesh, NewAffine(linear(rotationY(0.4), Vec(1.5, -2, 1)), Vec(0, 0, 0)))
	mustSceneInstance(t, scene, 12, mesh, NewAffine(Mat3{
		{-0.8, 0, 0},
		{0, 0.7, 0.3},
		{0, 0.2, 0.9},
	}, Vec(0, 0, 2)))

	rng := rand.New(rand.NewSource(99))
	for iteration := 0; iteration < 100; iteration++ {
		origin := Vec(rng.Float64()*8-4, rng.Float64()*8-4, rng.Float64()*8-4)
		direction := Vec(rng.Float64()*2-1, rng.Float64()*2-1, rng.Float64()*2-1)
		if direction.LengthSquared() == 0 {
			continue
		}
		ray := NewRay(origin, direction)
		options := PickOptions{CullBackfaces: true}
		got, gotOK := scene.PickWithOptions(ray, options)
		ref, refOK := referenceNearest(scene, ray, options)
		if gotOK != refOK {
			t.Fatalf("iteration %d culling hit mismatch: scene=%v reference=%v", iteration, gotOK, refOK)
		}
		if !gotOK {
			continue
		}
		if got.InstanceID != ref.InstanceID || got.TriangleID != ref.TriangleID ||
			got.FrontFace != ref.FrontFace ||
			math.Abs(got.T-ref.T) > 1e-10*math.Max(1, math.Abs(ref.T)) {
			t.Fatalf("iteration %d culling mismatch: scene=%+v reference=%+v", iteration, got, ref)
		}
	}
}

func TestTMinMaxTRange(t *testing.T) {
	mesh := unitXYTriangle()
	scene := NewScene()
	mustSceneInstance(t, scene, 1, mesh, IdentityAffine())
	ray := NewRay(Vec(0, 0, 1), Vec(0, 0, -1))

	if _, ok := scene.PickWithOptions(ray, PickOptions{TMin: 1.1, MaxT: 2}); ok {
		t.Fatal("TMin should reject the hit")
	}
	if _, ok := scene.PickWithOptions(ray, PickOptions{TMin: 0, MaxT: 0.9}); ok {
		t.Fatal("MaxT should reject the hit")
	}
	if _, ok := scene.PickWithOptions(ray, PickOptions{TMin: 0.9, MaxT: 1.1}); !ok {
		t.Fatal("in-range hit was rejected")
	}
}
