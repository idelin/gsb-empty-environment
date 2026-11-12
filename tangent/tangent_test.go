package tangent

import (
	"math"
	"testing"
)

const testEps = 1e-9

func near(a, b float64) bool { return math.Abs(a-b) < testEps }

func nearVec(a, b Vec3) bool {
	return near(a.X, b.X) && near(a.Y, b.Y) && near(a.Z, b.Z)
}

// quadCorners builds the standard two-triangle quad in the XY plane, normal
// +Z. When mirrored is true U runs backwards so the UV orientation is flipped
// while the geometry is unchanged.
func quadCorners(mirrored bool) []Corner {
	p0 := Vec3{X: 0, Y: 0}
	p1 := Vec3{X: 1, Y: 0}
	p2 := Vec3{X: 1, Y: 1}
	p3 := Vec3{X: 0, Y: 1}
	n := Vec3{Z: 1}
	uv := func(x, y float64) Vec2 {
		if mirrored {
			return Vec2{U: 1 - x, V: y}
		}
		return Vec2{U: x, V: y}
	}
	c := func(p Vec3, tuv Vec2) Corner {
		return Corner{Position: p, Normal: n, UV: tuv, Group: 1}
	}
	return []Corner{
		c(p0, uv(0, 0)), c(p1, uv(1, 0)), c(p2, uv(1, 1)),
		c(p0, uv(0, 0)), c(p2, uv(1, 1)), c(p3, uv(0, 1)),
	}
}

func TestPlaneRegularFrame(t *testing.T) {
	res, err := Generate(quadCorners(false), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Four shared corners collapse to four split vertices.
	if len(res.Vertices) != 4 {
		t.Fatalf("vertex count = %d, want 4", len(res.Vertices))
	}
	for i, v := range res.Vertices {
		if !nearVec(v.Normal, Vec3{Z: 1}) {
			t.Errorf("vertex %d normal = %v, want +Z", i, v.Normal)
		}
		if !nearVec(v.Tangent, Vec3{X: 1}) {
			t.Errorf("vertex %d tangent = %v, want +X", i, v.Tangent)
		}
		if !near(v.Handedness, 1) {
			t.Errorf("vertex %d handedness = %v, want +1", i, v.Handedness)
		}
		if !nearVec(Bitangent(v), Vec3{Y: 1}) {
			t.Errorf("vertex %d bitangent = %v, want +Y", i, Bitangent(v))
		}
		if math.Abs(Dot(v.Tangent, v.Normal)) > testEps {
			t.Errorf("vertex %d tangent not orthogonal to normal", i)
		}
	}
}

func TestPlaneMirroredFrame(t *testing.T) {
	res, err := Generate(quadCorners(true), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(res.Vertices) != 4 {
		t.Fatalf("vertex count = %d, want 4", len(res.Vertices))
	}
	for i, v := range res.Vertices {
		// Mirrored U means the UV tangent points along -X.
		if !nearVec(v.Tangent, Vec3{X: -1}) {
			t.Errorf("vertex %d tangent = %v, want -X", i, v.Tangent)
		}
		if !near(v.Handedness, -1) {
			t.Errorf("vertex %d handedness = %v, want -1", i, v.Handedness)
		}
		// The reconstructed bitangent must still follow +V, i.e. +Y, proving
		// the handedness sign keeps the frame coherent under mirroring.
		if !nearVec(Bitangent(v), Vec3{Y: 1}) {
			t.Errorf("vertex %d bitangent = %v, want +Y", i, Bitangent(v))
		}
	}
}

func TestPerturbedNormalPlane(t *testing.T) {
	res, err := Generate(quadCorners(false), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	v := res.Vertices[0]
	// A normal-map sample tilted in +X must perturb toward the +X tangent, and
	// a +Y tilt toward the +Y bitangent.
	tiltX := PerturbedNormal(v, Vec3{X: 1, Z: 1})
	if tiltX.X <= 0 || tiltX.Z <= 0 {
		t.Errorf("+X sample perturbed to %v, want positive X and Z", tiltX)
	}
	tiltY := PerturbedNormal(v, Vec3{Y: 1, Z: 1})
	if tiltY.Y <= 0 || tiltY.Z <= 0 {
		t.Errorf("+Y sample perturbed to %v, want positive Y and Z", tiltY)
	}
	// Flat sample reproduces the geometric normal.
	if !nearVec(PerturbedNormal(v, Vec3{Z: 1}), v.Normal) {
		t.Errorf("flat sample did not reproduce geometric normal")
	}

	// On the mirrored plane the same tangent-space +X sample tilts toward -X
	// in world space, which is the correct behavior across a mirror seam.
	mir, err := Generate(quadCorners(true), nil)
	if err != nil {
		t.Fatalf("generate mirrored: %v", err)
	}
	mv := mir.Vertices[0]
	mt := PerturbedNormal(mv, Vec3{X: 1, Z: 1})
	if mt.X >= 0 {
		t.Errorf("mirrored +X sample perturbed to %v, want negative X", mt)
	}
	my := PerturbedNormal(mv, Vec3{Y: 1, Z: 1})
	if my.Y <= 0 {
		t.Errorf("mirrored +Y sample perturbed to %v, want positive Y", my)
	}
}

func TestIndexAndSourceMapping(t *testing.T) {
	corners := quadCorners(false)
	res, err := Generate(corners, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(res.Indices) != len(corners) {
		t.Fatalf("index count = %d, want %d", len(res.Indices), len(corners))
	}
	seen := make(map[int]bool)
	for ci, vi := range res.Indices {
		if vi < 0 || vi >= len(res.Vertices) {
			t.Fatalf("corner %d mapped to out-of-range vertex %d", ci, vi)
		}
		seen[vi] = true
		// Source must point back at a real input corner.
		src := res.Vertices[vi].Source
		if src < 0 || src >= len(corners) {
			t.Fatalf("vertex %d source %d out of range", vi, src)
		}
	}
	if len(seen) != len(res.Vertices) {
		t.Errorf("some output vertices are unreferenced: %d vs %d", len(seen), len(res.Vertices))
	}
}

func TestValidation(t *testing.T) {
	if _, err := Generate(nil, nil); err == nil {
		t.Error("empty input should fail")
	}
	if _, err := Generate(quadCorners(false)[:2], nil); err == nil {
		t.Error("non-multiple-of-three input should fail")
	}
	singular := Mat4{}
	if _, err := Generate(quadCorners(false), &Options{Transform: &singular}); err == nil {
		t.Error("singular transform should fail")
	}
}
