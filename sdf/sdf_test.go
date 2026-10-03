package sdf

import (
	"math"
	"testing"
)

const tol = 1e-9

func almostEq(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestSphereSDF(t *testing.T) {
	s := Sphere(Vec3{0, 0, 0}, 1)
	if d := s(Vec3{0, 0, 0}); !almostEq(d, -1, tol) {
		t.Fatalf("center: got %v want -1", d)
	}
	if d := s(Vec3{1, 0, 0}); !almostEq(d, 0, tol) {
		t.Fatalf("surface: got %v want 0", d)
	}
	if d := s(Vec3{3, 0, 0}); !almostEq(d, 2, tol) {
		t.Fatalf("outside: got %v want 2", d)
	}
	if d := s(Vec3{0, 0.5, 0}); d >= 0 {
		t.Fatalf("interior point must be negative, got %v", d)
	}
}

func TestBoxSDF(t *testing.T) {
	b := Box(Vec3{0, 0, 0}, Vec3{1, 1, 1})
	if d := b(Vec3{0, 0, 0}); !almostEq(d, -1, tol) {
		t.Fatalf("center: got %v want -1", d)
	}
	if d := b(Vec3{2, 0, 0}); !almostEq(d, 1, tol) {
		t.Fatalf("face: got %v want 1", d)
	}
	// Corner (1,1,1): point (2,2,2) is sqrt(3) away.
	if d := b(Vec3{2, 2, 2}); !almostEq(d, math.Sqrt(3), 1e-9) {
		t.Fatalf("corner: got %v want %v", d, math.Sqrt(3))
	}
}

func TestCircle2SDF(t *testing.T) {
	c := Circle2(Vec2{1, 1}, 2)
	if d := c(Vec2{1, 1}); !almostEq(d, -2, tol) {
		t.Fatalf("center: got %v want -2", d)
	}
	if d := c(Vec2{1, 3}); !almostEq(d, 0, tol) {
		t.Fatalf("surface: got %v want 0", d)
	}
	if d := c(Vec2{1, 5}); !almostEq(d, 2, tol) {
		t.Fatalf("outside: got %v want 2", d)
	}
}

func TestUnionSubtract(t *testing.T) {
	a := Sphere(Vec3{-0.5, 0, 0}, 1)
	b := Sphere(Vec3{0.5, 0, 0}, 1)
	u := Union(a, b)
	if d := u(Vec3{0, 0, 0}); d >= 0 {
		t.Fatalf("union overlap point should be inside, got %v", d)
	}
	s := Subtract(a, b)
	// Origin is inside b, so it must be carved out of a.
	if d := s(Vec3{0, 0, 0}); d <= 0 {
		t.Fatalf("subtract should carve origin out, got %v", d)
	}
	// Far side of a is untouched.
	if d := s(Vec3{-1.5, 0, 0}); !almostEq(d, 0, tol) {
		t.Fatalf("untouched surface: got %v want 0", d)
	}
}

// Acceptance: rasterize a 64x64 signed distance grid from a geometry model.
func TestRasterize2_64x64(t *testing.T) {
	scene := Union2(
		Circle2(Vec2{0, 0}, 1),
		Box2(Vec2{2, 0}, Vec2{0.5, 0.5}),
	)
	g := Rasterize2(scene, 64, 64, Vec2{-3, -3}, Vec2{3, 3})
	if g.Nx != 64 || g.Ny != 64 || len(g.Data) != 64*64 {
		t.Fatalf("grid shape wrong: %dx%d len=%d", g.Nx, g.Ny, len(g.Data))
	}
	// Center node region must be inside the circle (negative).
	if d := g.Sample(Vec2{0, 0}); d >= 0 {
		t.Fatalf("origin should be inside circle, got %v", d)
	}
	// Far corner must be outside (positive).
	if d := g.Sample(Vec2{-2.9, -2.9}); d <= 0 {
		t.Fatalf("corner should be outside, got %v", d)
	}
	// Interpolated sample near the analytic value (grid res ~0.095).
	got := g.Sample(Vec2{1.7, 0})
	want := scene(Vec2{1.7, 0})
	if !almostEq(got, want, 0.1) {
		t.Fatalf("sample %v deviates from analytic %v", got, want)
	}
}

func TestRasterize3_64(t *testing.T) {
	scene := Union(
		Sphere(Vec3{0, 0, 0}, 1),
		Box(Vec3{2, 0, 0}, Vec3{0.5, 0.5, 0.5}),
	)
	g := Rasterize3(scene, 64, 64, 64, Vec3{-3, -3, -3}, Vec3{3, 3, 3})
	if len(g.Data) != 64*64*64 {
		t.Fatalf("grid size wrong: %d", len(g.Data))
	}
	if d := g.Sample(Vec3{0, 0, 0}); d >= 0 {
		t.Fatalf("origin should be inside sphere, got %v", d)
	}
	if d := g.Sample(Vec3{0, 0, 2.5}); d <= 0 {
		t.Fatalf("point should be outside, got %v", d)
	}
	got := g.Sample(Vec3{0, 0, 1.6})
	want := scene(Vec3{0, 0, 1.6})
	if !almostEq(got, want, 0.1) {
		t.Fatalf("sample %v deviates from analytic %v", got, want)
	}
}

func TestGradientApproximatesNormal(t *testing.T) {
	s := Sphere(Vec3{0, 0, 0}, 1)
	g := Rasterize3(s, 64, 64, 64, Vec3{-2, -2, -2}, Vec3{2, 2, 2})
	p := Vec3{1, 0, 0}
	n := g.Gradient(p).Norm()
	if math.Abs(n.X-1) > 0.05 || math.Abs(n.Y) > 0.05 || math.Abs(n.Z) > 0.05 {
		t.Fatalf("gradient at (1,0,0) should be ~+X, got %v", n)
	}
}

// Acceptance: ray marching from an arbitrary origin converges quickly
// onto the surface, stepping by the SDF value each iteration.
func TestRayMarch3HitSphere(t *testing.T) {
	s := Sphere(Vec3{0, 0, 0}, 1)
	h := RayMarch3(s, Vec3{5, 0, 0}, Vec3{-1, 0, 0}, MarchOptions{})
	if !h.Hit {
		t.Fatal("ray should hit sphere")
	}
	if !almostEq(h.Distance, 4, 1e-3) {
		t.Fatalf("travel distance: got %v want ~4", h.Distance)
	}
	if !almostEq(h.Point.Len(), 1, 1e-3) {
		t.Fatalf("hit point should lie on surface, |p|=%v", h.Point.Len())
	}
	if h.Steps > 20 {
		t.Fatalf("sphere tracing should converge fast, took %d steps", h.Steps)
	}
}

func TestRayMarch3ArbitraryDirection(t *testing.T) {
	scene := Union(
		Sphere(Vec3{0, 0, 0}, 1),
		Box(Vec3{3, 0, 0}, Vec3{0.5, 0.5, 0.5}),
	)
	origins := []Vec3{
		{0, 5, 0}, {5, 5, 5}, {-4, 2, 1}, {0, 0, -6},
	}
	for _, o := range origins {
		dir := (Vec3{0, 0, 0}).Sub(o) // aim at the sphere at origin
		h := RayMarch3(scene, o, dir, MarchOptions{})
		if !h.Hit {
			t.Fatalf("ray from %v should hit", o)
		}
		if d := scene(h.Point); math.Abs(d) > 1e-3 {
			t.Fatalf("hit point from %v not on surface: sdf=%v", o, d)
		}
		if h.Steps > 32 {
			t.Fatalf("too many steps from %v: %d", o, h.Steps)
		}
	}
}

func TestRayMarch3Miss(t *testing.T) {
	s := Sphere(Vec3{0, 0, 0}, 1)
	h := RayMarch3(s, Vec3{5, 5, 0}, Vec3{1, 0, 0}, MarchOptions{MaxDist: 20})
	if h.Hit {
		t.Fatal("parallel ray far from sphere should miss")
	}
}

func TestRayMarch2HitCircle(t *testing.T) {
	c := Circle2(Vec2{0, 0}, 1)
	h := RayMarch2(c, Vec2{-4, 0}, Vec2{1, 0}, MarchOptions{})
	if !h.Hit {
		t.Fatal("ray should hit circle")
	}
	if !almostEq(h.Distance, 3, 1e-3) {
		t.Fatalf("travel distance: got %v want ~3", h.Distance)
	}
	if !almostEq(h.Point.Len(), 1, 1e-3) {
		t.Fatalf("hit point should lie on circle, |p|=%v", h.Point.Len())
	}
}

// Acceptance: marching against the rasterized 64x64 grid also converges
// onto the surface.
func TestRayMarchGrid2_64x64(t *testing.T) {
	scene := Circle2(Vec2{0, 0}, 1)
	g := Rasterize2(scene, 64, 64, Vec2{-3, -3}, Vec2{3, 3})
	h := RayMarchGrid2(g, Vec2{0, 2.8}, Vec2{0, -1}, MarchOptions{Epsilon: 1e-3})
	if !h.Hit {
		t.Fatal("ray should hit circle via grid")
	}
	if !almostEq(h.Distance, 1.8, 0.05) {
		t.Fatalf("travel distance: got %v want ~1.8", h.Distance)
	}
	if h.Steps > 32 {
		t.Fatalf("grid march should converge fast, took %d steps", h.Steps)
	}
}

func TestRayMarchGrid3(t *testing.T) {
	scene := Sphere(Vec3{0, 0, 0}, 1)
	g := Rasterize3(scene, 64, 64, 64, Vec3{-3, -3, -3}, Vec3{3, 3, 3})
	h := RayMarchGrid3(g, Vec3{2.5, 0, 0}, Vec3{-1, 0, 0}, MarchOptions{Epsilon: 1e-3})
	if !h.Hit {
		t.Fatal("ray should hit sphere via grid")
	}
	if !almostEq(h.Distance, 1.5, 0.05) {
		t.Fatalf("travel distance: got %v want ~1.5", h.Distance)
	}
}
