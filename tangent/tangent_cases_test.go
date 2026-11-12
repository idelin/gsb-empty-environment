package tangent

import (
	"math"
	"testing"
)

// cylinderCorners builds a tube of segs angular slices along the Z axis. The
// theta=0 seam is duplicated as independent grid columns carrying UVs U=0 and
// U=1, with every slice using the same winding. When mirrorU is true U is
// reversed, flipping UV orientation while the geometry stays the same.
func cylinderCorners(segs int, mirrorU bool) []Corner {
	var corners []Corner
	uAt := func(theta float64) float64 {
		u := theta / (2 * math.Pi)
		if mirrorU {
			return 1 - u
		}
		return u
	}
	grid := func(i int, z, v float64) Corner {
		theta := 2 * math.Pi * float64(i) / float64(segs)
		n := Vec3{X: math.Cos(theta), Y: math.Sin(theta)}
		return Corner{
			Position: Vec3{X: n.X, Y: n.Y, Z: z},
			Normal:   n,
			UV:       Vec2{U: uAt(theta), V: v},
			Group:    1,
		}
	}
	for i := 0; i < segs; i++ {
		j := i + 1
		corners = append(corners,
			grid(i, 0, 0), grid(j, 0, 0), grid(j, 1, 1),
			grid(i, 0, 0), grid(j, 1, 1), grid(i, 1, 1),
		)
	}
	return corners
}

func seamVertices(t *testing.T, res *Result) []Vertex {
	t.Helper()
	var seam []Vertex
	for _, v := range res.Vertices {
		if near(v.Position.X, 1) && near(v.Position.Y, 0) {
			seam = append(seam, v)
		}
	}
	// Two rings (z=0 and z=1), each split into independent U=0 / U=1 sides.
	if len(seam) != 4 {
		t.Fatalf("seam split vertices = %d, want 4", len(seam))
	}
	return seam
}

func TestCylinderSeam(t *testing.T) {
	res, err := Generate(cylinderCorners(8, false), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i, v := range seamVertices(t, res) {
		// U increases with theta, so the tangent follows +Y at theta=0.
		if !nearVec(v.Tangent, Vec3{Y: 1}) {
			t.Errorf("seam vertex %d tangent = %v, want +Y", i, v.Tangent)
		}
		if !near(v.Handedness, 1) {
			t.Errorf("seam vertex %d handedness = %v, want +1", i, v.Handedness)
		}
		// Bitangent follows the cylinder axis +Z.
		if !nearVec(Bitangent(v), Vec3{Z: 1}) {
			t.Errorf("seam vertex %d bitangent = %v, want +Z", i, Bitangent(v))
		}
		if math.Abs(Dot(v.Tangent, v.Normal)) > testEps {
			t.Errorf("seam vertex %d tangent not orthogonal to normal", i)
		}
		// A tangent-space +X normal sample tilts the world normal toward +Y.
		pert := PerturbedNormal(v, Vec3{X: 1, Z: 1})
		if pert.Y <= 0 {
			t.Errorf("seam +X sample = %v, want positive Y", pert)
		}
	}
}

func TestCylinderMirroredSeam(t *testing.T) {
	res, err := Generate(cylinderCorners(8, true), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i, v := range seamVertices(t, res) {
		// Reversed U: tangent follows -Y, but V still runs up +Z.
		if !nearVec(v.Tangent, Vec3{Y: -1}) {
			t.Errorf("mirrored seam vertex %d tangent = %v, want -Y", i, v.Tangent)
		}
		if !near(v.Handedness, -1) {
			t.Errorf("mirrored seam vertex %d handedness = %v, want -1", i, v.Handedness)
		}
		if !nearVec(Bitangent(v), Vec3{Z: 1}) {
			t.Errorf("mirrored seam vertex %d bitangent = %v, want +Z", i, Bitangent(v))
		}
		pert := PerturbedNormal(v, Vec3{X: 1, Z: 1})
		if pert.Y >= 0 {
			t.Errorf("mirrored seam +X sample = %v, want negative Y", pert)
		}
	}
}

func TestNegativeScaleInstance(t *testing.T) {
	// Mirror the instance along X. Geometry flips, normals follow through the
	// inverse-transpose, and UV handedness stays consistent in world space.
	mirrorX := Mat4{
		-1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
	res, err := Generate(quadCorners(false), &Options{Transform: &mirrorX})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(res.Vertices) != 4 {
		t.Fatalf("vertex count = %d, want 4", len(res.Vertices))
	}
	for i, v := range res.Vertices {
		// A mirror about the YZ plane flips X only; the +Z surface normal is
		// unchanged, while the U axis reverses.
		if !nearVec(v.Normal, Vec3{Z: 1}) {
			t.Errorf("vertex %d normal = %v, want +Z", i, v.Normal)
		}
		// U direction in world space is now -X.
		if !nearVec(v.Tangent, Vec3{X: -1}) {
			t.Errorf("vertex %d tangent = %v, want -X", i, v.Tangent)
		}
		// V still runs +Y; the reconstructed bitangent must match.
		// n × t = +Z × -X = -Y, then flipped by the negative handedness back
		// to +Y, matching the unchanged V direction.
		if !nearVec(Bitangent(v), Vec3{Y: 1}) {
			t.Errorf("vertex %d bitangent = %v, want +Y", i, Bitangent(v))
		}
		if !near(v.Handedness, -1) {
			t.Errorf("vertex %d handedness = %v, want -1", i, v.Handedness)
		}
		flat := PerturbedNormal(v, Vec3{Z: 1})
		if !nearVec(flat, Vec3{Z: 1}) {
			t.Errorf("vertex %d flat sample = %v, want +Z", i, flat)
		}
		tilt := PerturbedNormal(v, Vec3{Y: 1, Z: 1})
		if tilt.Y <= 0 {
			t.Errorf("vertex %d +Y tilt = %v, want positive Y", i, tilt)
		}
	}
}

func TestDegenerateUV(t *testing.T) {
	// All UVs collapse to one point: no tangent can be derived from UVs, so
	// the deterministic normal-orthogonal fallback applies.
	corners := quadCorners(false)
	for i := range corners {
		corners[i].UV = Vec2{}
	}
	res, err := Generate(corners, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	seen := map[Vec3]bool{}
	for i, v := range res.Vertices {
		if math.Abs(Dot(v.Tangent, v.Normal)) > testEps {
			t.Errorf("vertex %d fallback tangent not orthogonal to normal", i)
		}
		if l := v.Tangent.Length(); !near(l, 1) {
			t.Errorf("vertex %d fallback tangent length = %v, want 1", i, l)
		}
		if !near(v.Handedness, 1) {
			t.Errorf("vertex %d fallback handedness = %v, want +1", i, v.Handedness)
		}
		seen[v.Position] = true
	}
	// Deterministic: a second run produces identical tangents.
	res2, err := Generate(corners, nil)
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
	for i := range res.Vertices {
		if !nearVec(res.Vertices[i].Tangent, res2.Vertices[i].Tangent) {
			t.Fatalf("fallback tangent not deterministic at vertex %d", i)
		}
	}
}

func TestFacetedAndSmoothingGroups(t *testing.T) {
	// Group zero facets every corner even though positions/UVs coincide.
	faceted := quadCorners(false)
	for i := range faceted {
		faceted[i].Group = 0
	}
	res, err := Generate(faceted, nil)
	if err != nil {
		t.Fatalf("generate faceted: %v", err)
	}
	if len(res.Vertices) != len(faceted) {
		t.Fatalf("faceted vertex count = %d, want %d", len(res.Vertices), len(faceted))
	}

	// Different smoothing groups at one shared corner force a split too.
	split := quadCorners(false)
	split[3].Group = 2
	split[4].Group = 2
	split[5].Group = 2
	res2, err := Generate(split, nil)
	if err != nil {
		t.Fatalf("generate split groups: %v", err)
	}
	// Both shared corners of the triangles (p0 and p2) change group, so each
	// splits into two copies while p1 and p3 remain single: 6 vertices total.
	if len(res2.Vertices) != 6 {
		t.Fatalf("group-split vertex count = %d, want 6", len(res2.Vertices))
	}
}

// TestNoAveragingAcrossMirrorSeam places a regular-UV triangle and a
// mirrored-UV triangle on the same geometric corner. The two orientations must
// produce separate split vertices with opposite handedness rather than a
// single averaged, nearly-zero tangent.
func TestNoAveragingAcrossMirrorSeam(t *testing.T) {
	n := Vec3{Z: 1}
	shared := Vec3{X: 0, Y: 0}
	right := Vec3{X: 1, Y: 0}
	up := Vec3{X: 0, Y: 1}
	corners := []Corner{
		// Regular orientation: U maps to +X.
		{Position: shared, Normal: n, UV: Vec2{U: 0, V: 0}, Group: 1},
		{Position: right, Normal: n, UV: Vec2{U: 1, V: 0}, Group: 1},
		{Position: up, Normal: n, UV: Vec2{U: 0, V: 1}, Group: 1},
		// Mirrored orientation with identical winding: U is reversed so it
		// maps to -X, while the shared corner keeps the same position and UV.
		{Position: shared, Normal: n, UV: Vec2{U: 0, V: 0}, Group: 1},
		{Position: right, Normal: n, UV: Vec2{U: -1, V: 0}, Group: 1},
		{Position: up, Normal: n, UV: Vec2{U: 0, V: 1}, Group: 1},
	}
	res, err := Generate(corners, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var atShared []Vertex
	for _, v := range res.Vertices {
		if nearVec(v.Position, shared) {
			atShared = append(atShared, v)
		}
	}
	if len(atShared) != 2 {
		t.Fatalf("shared corner splits = %d, want 2 (no cross-seam averaging)", len(atShared))
	}

	regular, mirrored := atShared[0], atShared[1]
	if regular.Handedness == mirrored.Handedness {
		t.Fatalf("handedness values equal (%v); want opposite signs", regular.Handedness)
	}
	if nearVec(regular.Tangent, mirrored.Tangent) {
		t.Fatalf("tangents equal %v; want opposite directions", regular.Tangent)
	}
	// Each frame stays orthonormal and each bitangent still tracks +V (+Y).
	for i, v := range atShared {
		if math.Abs(Dot(v.Tangent, v.Normal)) > testEps {
			t.Errorf("split %d tangent not orthogonal to normal", i)
		}
		if !nearVec(Bitangent(v), Vec3{Y: 1}) {
			t.Errorf("split %d bitangent = %v, want +Y", i, Bitangent(v))
		}
	}
}
