package sdf

import (
	"math"
	"testing"
)

// TestContourCircle extracts the zero level set of a sampled circle
// and checks every vertex lies on the analytic circle and the total
// contour length approximates the circumference.
func TestContourCircle(t *testing.T) {
	c := Circle{Center: V2(0, 0), Radius: 0.8}
	g := Sample2(c, 64, 64, V2(-1.5, -1.5), V2(1.5, 1.5))
	segs := g.Contour(0)
	if len(segs) == 0 {
		t.Fatal("no contour segments extracted")
	}
	h := 3.0 / 63
	total := 0.0
	for _, s := range segs {
		for _, p := range [2]Vec2{s.A, s.B} {
			if r := p.Len(); math.Abs(r-0.8) > h {
				t.Fatalf("contour vertex at radius %v, want ~0.8", r)
			}
		}
		total += s.B.Sub(s.A).Len()
	}
	want := 2 * math.Pi * 0.8
	if math.Abs(total-want)/want > 0.05 {
		t.Fatalf("contour length %v, want ~%v", total, want)
	}
}

// TestContourEmptyGrid: a field with no zero crossing yields nothing.
func TestContourEmptyGrid(t *testing.T) {
	c := Circle{Center: V2(10, 10), Radius: 1}
	g := Sample2(c, 64, 64, V2(-1, -1), V2(1, 1))
	if segs := g.Contour(0); len(segs) != 0 {
		t.Fatalf("expected empty contour, got %d segments", len(segs))
	}
}

// TestSurfaceSphere extracts the iso-surface of a sampled sphere with
// marching tetrahedra and validates vertex positions and total area.
func TestSurfaceSphere(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	g := Sample3(s, 48, 48, 48, V3(-1.5, -1.5, -1.5), V3(1.5, 1.5, 1.5))
	tris := g.Surface(0)
	if len(tris) == 0 {
		t.Fatal("no surface triangles extracted")
	}
	h := 3.0 / 47
	area := 0.0
	for _, tri := range tris {
		for _, p := range [3]Vec3{tri.A, tri.B, tri.C} {
			if r := p.Len(); math.Abs(r-1) > h {
				t.Fatalf("surface vertex at radius %v, want ~1", r)
			}
		}
		area += tri.Area()
	}
	want := 4 * math.Pi
	if math.Abs(area-want)/want > 0.1 {
		t.Fatalf("surface area %v, want ~%v", area, want)
	}
}

// TestSurfaceNormalsPointOutward verifies triangle windings: each
// triangle normal must roughly align with the sphere's outward normal.
func TestSurfaceNormalsPointOutward(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	g := Sample3(s, 32, 32, 32, V3(-1.5, -1.5, -1.5), V3(1.5, 1.5, 1.5))
	for _, tri := range g.Surface(0) {
		outward := tri.Centroid().Normalize()
		if tri.Normal().Dot(outward) <= 0 {
			t.Fatalf("triangle normal points inward at %v", tri.Centroid())
		}
	}
}

// TestSurfaceEmptyGrid: no zero crossing -> no triangles.
func TestSurfaceEmptyGrid(t *testing.T) {
	s := Sphere{Center: V3(10, 0, 0), Radius: 1}
	g := Sample3(s, 16, 16, 16, V3(-1, -1, -1), V3(1, 1, 1))
	if tris := g.Surface(0); len(tris) != 0 {
		t.Fatalf("expected empty surface, got %d triangles", len(tris))
	}
}
