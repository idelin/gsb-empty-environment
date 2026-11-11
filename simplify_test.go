package meshsimp

import (
	"math"
	"testing"
)

func TestSphereReachesTargetFaceCount(t *testing.T) {
	src := newUVSphere(1, 48, 24)
	if !src.IsManifold() {
		t.Fatalf("test fixture: sphere must be manifold")
	}

	target := 500
	res, err := Simplify(src, Options{TargetFaces: target})
	if err != nil {
		t.Fatalf("Simplify returned error: %v", err)
	}
	if !res.ReachedTarget || len(res.Mesh.Faces) != target {
		t.Fatalf("faces = %d, want exactly %d (reason: %q)",
			len(res.Mesh.Faces), target, res.StopReason)
	}
	if len(res.Mesh.Faces) >= len(src.Faces) {
		t.Fatalf("mesh was not simplified: %d -> %d", len(src.Faces), len(res.Mesh.Faces))
	}
	if !res.Mesh.IsManifold() {
		t.Fatalf("simplified sphere lost manifoldness")
	}
	if got := boundaryEdgeCount(res.Mesh); got != 0 {
		t.Fatalf("closed sphere gained %d boundary edges", got)
	}
}

func TestSphereErrorBound(t *testing.T) {
	src := newUVSphere(1, 64, 32)
	res, err := Simplify(src, Options{TargetFaces: 1200})
	if err != nil {
		t.Fatalf("Simplify returned error: %v", err)
	}

	report := DistanceToMesh(src, res.Mesh, 3)
	haus := SymmetricHausdorff(src, res.Mesh, 3)

	// The true surface is the unit sphere, so every output vertex must stay
	// close to radius 1 as an independent check.
	for i, p := range res.Mesh.Positions {
		r := norm(p)
		if math.Abs(r-1) > 0.05 {
			t.Fatalf("vertex %d radius %.4f deviates > 0.05 from unit sphere", i, r)
		}
	}
	if report.RMS > 0.02 {
		t.Fatalf("RMS distance %.5f exceeds bound 0.02", report.RMS)
	}
	if report.Max > 0.05 || haus > 0.05 {
		t.Fatalf("max distance %.5f / hausdorff %.5f exceeds bound 0.05",
			report.Max, haus)
	}
}

func TestSphereMonotonicErrorAndStaleEntries(t *testing.T) {
	src := newUVSphere(1, 48, 24)
	errs := make([]float64, 0, 4)
	for _, target := range []int{1800, 1200, 700, 300} {
		res, err := Simplify(src, Options{TargetFaces: target})
		if err != nil {
			t.Fatalf("Simplify(%d): %v", target, err)
		}
		report := DistanceToMesh(src, res.Mesh, 2)
		errs = append(errs, report.Max)
	}
	// Sampled Hausdorff distances are not bit-for-bit monotone across
	// independent runs, but the coarsest mesh should not beat the finest by a
	// meaningful margin. Allow a small relative tolerance for sampling noise.
	if errs[len(errs)-1] < errs[0]*0.9 {
		t.Fatalf("expected non-decreasing error trend, got %v", errs)
	}
	// And the absolute values must stay small for a well-behaved QEM run.
	for i, e := range errs {
		if e > 0.06 {
			t.Fatalf("error %d = %.5f exceeds bound 0.06", i, e)
		}
	}

	// Any non-trivial run invalidates candidates, so stale heap entries must
	// be observed and filtered without corrupting results.
	res, err := Simplify(src, Options{TargetFaces: 400})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if res.Rejects.StaleEntries == 0 {
		t.Fatalf("expected stale heap entries to be detected, got 0")
	}
	if !res.Mesh.IsManifold() {
		t.Fatalf("output not manifold after stale filtering")
	}
}

func TestHoledPlanePreservesBoundaryAndTopology(t *testing.T) {
	src := newHoledPlane(32)
	if !src.IsManifold() {
		t.Fatalf("test fixture: holed plane must be manifold")
	}
	origEdges := src.BoundaryEdges()
	origVerts := boundaryVertexCount(src)
	if len(origEdges) != origVerts {
		t.Fatalf("boundary loops must be closed: %d edges vs %d vertices",
			len(origEdges), origVerts)
	}

	res, err := Simplify(src, Options{TargetFaces: 200})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	out := res.Mesh
	if !out.IsManifold() {
		t.Fatalf("simplified plane is not manifold")
	}

	// Two boundary loops, same edge count, and every original boundary edge
	// must still exist with unchanged endpoint coordinates.
	if got := boundaryEdgeCount(out); got != len(origEdges) {
		t.Fatalf("boundary edge count changed: %d -> %d", len(origEdges), got)
	}
	if got := boundaryVertexCount(out); got != origVerts {
		t.Fatalf("boundary vertex count changed: %d -> %d", origVerts, got)
	}

	posByCoord := make(map[[3]float64]int, len(out.Positions))
	for i, p := range out.Positions {
		posByCoord[p] = i
	}
	for key := range origEdges {
		a, b := keyEnds(key)
		pa, pb := src.Positions[a], src.Positions[b]
		ia, okA := posByCoord[pa]
		ib, okB := posByCoord[pb]
		if !okA || !okB {
			t.Fatalf("boundary vertex removed during simplification")
		}
		if _, ok := out.BoundaryEdges()[edgeKey(ia, ib)]; !ok {
			t.Fatalf("boundary edge %v-%v disappeared", pa, pb)
		}
	}

	// All output faces stay in the z=0 plane with unchanged +z winding.
	for _, f := range out.Faces {
		n := faceNormal(out.Positions, f)
		if math.Abs(n[2]-1) > 1e-9 || math.Abs(n[0]) > 1e-9 || math.Abs(n[1]) > 1e-9 {
			t.Fatalf("face normal %v is no longer +z", n)
		}
	}
}

func TestHoledPlaneErrorBound(t *testing.T) {
	src := newHoledPlane(32)
	res, err := Simplify(src, Options{TargetFaces: 300})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	// The plane is perfectly flat, so a topology-preserving simplification
	// must introduce essentially zero geometric error. Measure from the
	// simplified surface back toward the source so hole interior samples do
	// not pollute the result.
	report := DistanceToMesh(res.Mesh, src, 3)
	if report.Max > 1e-9 {
		t.Fatalf("flat plane simplification introduced error %.3e", report.Max)
	}
}

func TestHoledPlaneCannotReachImpossibleTarget(t *testing.T) {
	src := newHoledPlane(12)
	res, err := Simplify(src, Options{TargetFaces: 4})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if res.ReachedTarget {
		t.Fatalf("boundary-locked mesh should not reach 4 faces, got %d",
			len(res.Mesh.Faces))
	}
	if res.StopReason == "" {
		t.Fatalf("expected an explanatory stop reason")
	}
	if res.Rejects.Boundary == 0 {
		t.Fatalf("expected boundary rejections to be counted")
	}
	if !res.Mesh.IsManifold() {
		t.Fatalf("failed run must still return a valid manifold mesh")
	}
	if got := boundaryEdgeCount(res.Mesh); got != boundaryEdgeCount(src) {
		t.Fatalf("boundary changed on failed run: %d -> %d",
			boundaryEdgeCount(src), got)
	}
}

func TestAttributesInterpolatedAndInputPreserved(t *testing.T) {
	src := newUVSphere(1, 24, 12)
	before := len(src.Positions)
	res, err := Simplify(src, Options{TargetFaces: 200})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if len(src.Positions) != before || len(src.Faces) == len(res.Mesh.Faces) {
		t.Fatalf("input mesh was mutated by Simplify")
	}

	out := res.Mesh
	if len(out.Normals) != len(out.Positions) ||
		len(out.UVs) != len(out.Positions) ||
		len(out.Colors) != len(out.Positions) {
		t.Fatalf("attribute slices not compacted with positions")
	}
	for i, n := range out.Normals {
		if d := math.Abs(norm(n) - 1); d > 1e-9 {
			t.Fatalf("normal %d not unit length after renormalization: %.6f", i, norm(n))
		}
	}
	for i, c := range out.Colors {
		if d := math.Abs(norm(c)); d < 1e-12 {
			t.Fatalf("color %d collapsed to zero vector", i)
		}
	}
	// UVs stay inside their parameterization domain.
	for i, uv := range out.UVs {
		if uv[0] < -1e-9 || uv[0] > 1+1e-9 || uv[1] < -1e-9 || uv[1] > 1+1e-9 {
			t.Fatalf("uv %d out of range: %v", i, uv)
		}
	}
}

func TestTetrahedronFloorAndRejectReasons(t *testing.T) {
	// A closed tetrahedron can collapse one edge (the two faces sharing it
	// die) but the remaining two triangles share an edge and cannot collapse
	// further without becoming degenerate.
	m := &Mesh{
		Positions: [][3]float64{
			{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1},
		},
		Faces: [][3]int{
			{0, 2, 1}, {0, 1, 3}, {0, 3, 2}, {1, 2, 3},
		},
	}
	res, err := Simplify(m, Options{TargetFaces: 2})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if len(res.Mesh.Faces) != 2 {
		t.Fatalf("tetrahedron should bottom out at 2 faces, got %d",
			len(res.Mesh.Faces))
	}
	if !res.ReachedTarget {
		t.Fatalf("2-face target should be reachable, reason: %q", res.StopReason)
	}

	// Going below 2 faces must fail with an explicit reason.
	res2, err := Simplify(m, Options{TargetFaces: 1})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if res2.ReachedTarget || res2.StopReason == "" {
		t.Fatalf("expected failure with reason below the 2-face floor")
	}
	if !res2.Mesh.IsManifold() {
		t.Fatalf("failed run must still return a manifold mesh")
	}
}

func TestMaxErrorStop(t *testing.T) {
	src := newUVSphere(1, 48, 24)
	res, err := Simplify(src, Options{TargetFaces: 100, MaxError: 1e-8})
	if err != nil {
		t.Fatalf("Simplify: %v", err)
	}
	if res.ReachedTarget {
		t.Fatalf("tight MaxError should stop before 100 faces")
	}
	if res.StopReason == "" {
		t.Fatalf("expected MaxError stop reason")
	}
	if !res.Mesh.IsManifold() {
		t.Fatalf("early-stopped mesh must stay manifold")
	}
}

func TestNonManifoldInputRejected(t *testing.T) {
	// Validate must reject faces that reference missing vertices.
	m := &Mesh{
		Positions: [][3]float64{
			{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0},
		},
		Faces: [][3]int{
			{0, 1, 2},
			{0, 1, 9},
		},
	}
	if _, err := Simplify(m, Options{TargetFaces: 1}); err == nil {
		t.Fatalf("expected validation error for out-of-range face index")
	}
}

func TestNonManifoldEdgeIsDetected(t *testing.T) {
	// Two triangles share edge (0,1) with the same winding direction, which
	// is an inconsistently oriented, hence non-manifold, configuration.
	m := &Mesh{
		Positions: [][3]float64{
			{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 1},
		},
		Faces: [][3]int{
			{0, 1, 2},
			{0, 1, 3},
		},
	}
	if m.IsManifold() {
		t.Fatalf("fixture should report as non-manifold")
	}
}
