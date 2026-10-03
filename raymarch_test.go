package sdf

import (
	"math"
	"testing"
)

// TestRayMarchSphere verifies sphere tracing converges on the surface
// at the analytically correct distance, in very few steps.
func TestRayMarchSphere(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	res := RayMarch3(s, V3(3, 0, 0), V3(-1, 0, 0), MarchOptions{})
	if !res.Hit {
		t.Fatal("expected hit")
	}
	if !almostEq(res.T, 2, 1e-3) {
		t.Fatalf("hit distance: got %v want ~2", res.T)
	}
	if !almostEq(res.Position.X, 1, 1e-3) {
		t.Fatalf("hit point x: got %v want ~1", res.Position.X)
	}
	// Sphere tracing a head-on ray needs only ~2 evaluations.
	if res.Steps > 4 {
		t.Fatalf("too many steps: %d", res.Steps)
	}
}

// TestRayMarchFastConvergence is the acceptance boundary: from an
// arbitrary origin, every step leaps by the SDF value (the maximum
// safe empty distance) and the march converges rapidly.
func TestRayMarchFastConvergence(t *testing.T) {
	scene := Union3(
		Sphere{Center: V3(0, 0, 0), Radius: 1},
		Box3{Center: V3(1.6, 0, 0), Half: V3(0.5, 0.5, 0.5)},
	)
	origins := []Vec3{
		V3(5, 2, 1), V3(-4, 0.3, 2), V3(0.5, 6, -1), V3(-3, -3, -3),
	}
	target := V3(0.4, 0, 0)
	for _, ro := range origins {
		rd := target.Sub(ro).Normalize()
		res := RayMarch3(scene, ro, rd, MarchOptions{MaxDist: 50})
		if !res.Hit {
			t.Fatalf("ray from %v missed", ro)
		}
		if d := scene.Distance(res.Position); math.Abs(d) > 2e-3 {
			t.Fatalf("hit point not on surface: sdf=%v", d)
		}
		if res.Steps > 32 {
			t.Fatalf("ray from %v took %d steps, want fast convergence", ro, res.Steps)
		}
	}
}

func TestRayMarchMiss(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	res := RayMarch3(s, V3(3, 0, 0), V3(0, 1, 0), MarchOptions{MaxDist: 20})
	if res.Hit {
		t.Fatal("expected miss")
	}
}

func TestRayMarch2Circle(t *testing.T) {
	c := Circle{Center: V2(0, 0), Radius: 0.5}
	res := RayMarch2(c, V2(2, 0), V2(-1, 0), MarchOptions{})
	if !res.Hit {
		t.Fatal("expected hit")
	}
	if !almostEq(res.T, 1.5, 1e-3) {
		t.Fatalf("hit distance: got %v want ~1.5", res.T)
	}
}

// TestRayMarchRasterizedGrid marches the sampled 64x64x64 grid itself
// (trilinear interpolation) instead of the analytic field.
func TestRayMarchRasterizedGrid(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	g := Sample3(s, 64, 64, 64, V3(-2, -2, -2), V3(2, 2, 2))
	res := RayMarch3(g, V3(3, 0.2, 0.1), V3(-1, -0.066, -0.033).Normalize(),
		MarchOptions{StepScale: 0.9, Epsilon: 1e-3})
	if !res.Hit {
		t.Fatal("expected hit on rasterized grid")
	}
	if r := res.Position.Len(); math.Abs(r-1) > 0.05 {
		t.Fatalf("hit radius: got %v want ~1 (grid resolution limited)", r)
	}
	if res.Steps > 64 {
		t.Fatalf("too many steps on grid: %d", res.Steps)
	}
}

func TestNormals(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	n := Normal3(s, V3(1, 0, 0))
	if !almostEq(n.X, 1, 1e-3) || !almostEq(n.Y, 0, 1e-3) || !almostEq(n.Z, 0, 1e-3) {
		t.Fatalf("normal at (1,0,0): got %v", n)
	}
	n = Normal3(s, V3(0, -1, 0))
	if !almostEq(n.Y, -1, 1e-3) {
		t.Fatalf("normal at (0,-1,0): got %v", n)
	}
	c := Circle{Center: V2(0, 0), Radius: 1}
	n2 := Normal2(c, V2(0, 1))
	if !almostEq(n2.Y, 1, 1e-3) {
		t.Fatalf("2D normal at (0,1): got %v", n2)
	}
}
