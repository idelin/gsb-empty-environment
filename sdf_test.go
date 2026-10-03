package sdf

import (
	"math"
	"testing"
)

func almostEq(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestCircleDistance(t *testing.T) {
	c := Circle{Center: V2(0, 0), Radius: 1}
	if d := c.Distance(V2(3, 4)); !almostEq(d, 4, 1e-12) {
		t.Fatalf("outside: got %v want 4", d)
	}
	if d := c.Distance(V2(0, 0)); !almostEq(d, -1, 1e-12) {
		t.Fatalf("center: got %v want -1", d)
	}
	if d := c.Distance(V2(0.6, 0.8)); !almostEq(d, 0, 1e-12) {
		t.Fatalf("on surface: got %v want 0", d)
	}
}

func TestBox2Distance(t *testing.T) {
	b := Box2{Center: V2(0, 0), Half: V2(1, 1)}
	if d := b.Distance(V2(3, 0)); !almostEq(d, 2, 1e-12) {
		t.Fatalf("face: got %v want 2", d)
	}
	if d := b.Distance(V2(3, 4)); !almostEq(d, math.Sqrt(13), 1e-12) {
		t.Fatalf("corner: got %v want sqrt(13)", d)
	}
	if d := b.Distance(V2(0, 0)); !almostEq(d, -1, 1e-12) {
		t.Fatalf("center: got %v want -1", d)
	}
}

func TestSphereAndBox3(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 2}
	if d := s.Distance(V3(0, 0, 5)); !almostEq(d, 3, 1e-12) {
		t.Fatalf("sphere outside: got %v want 3", d)
	}
	if d := s.Distance(V3(0, 0, 0)); !almostEq(d, -2, 1e-12) {
		t.Fatalf("sphere center: got %v want -2", d)
	}
	b := Box3{Center: V3(0, 0, 0), Half: V3(1, 1, 1)}
	if d := b.Distance(V3(2, 0, 0)); !almostEq(d, 1, 1e-12) {
		t.Fatalf("box face: got %v want 1", d)
	}
	if d := b.Distance(V3(2, 2, 2)); !almostEq(d, math.Sqrt(3), 1e-12) {
		t.Fatalf("box corner: got %v want sqrt(3)", d)
	}
	if d := b.Distance(V3(0.5, 0, 0)); !almostEq(d, -0.5, 1e-12) {
		t.Fatalf("box inside: got %v want -0.5", d)
	}
}

func TestPlane3(t *testing.T) {
	pl := Plane3{Point: V3(0, 0, 0), Normal: V3(0, 1, 0)}
	if d := pl.Distance(V3(5, 2, -7)); !almostEq(d, 2, 1e-12) {
		t.Fatalf("above plane: got %v want 2", d)
	}
	if d := pl.Distance(V3(0, -3, 0)); !almostEq(d, -3, 1e-12) {
		t.Fatalf("below plane: got %v want -3", d)
	}
}

func TestCSG2(t *testing.T) {
	a := Circle{Center: V2(-1, 0), Radius: 1}
	b := Circle{Center: V2(1, 0), Radius: 1}
	u := Union2(a, b)
	if d := u.Distance(V2(0, 0)); !almostEq(d, 0, 1e-12) {
		t.Fatalf("union at overlap boundary: got %v want 0", d)
	}
	sub := Subtract2(a, b)
	// Point (1,0) is the center of b, so a\b must be positive there.
	if d := sub.Distance(V2(1, 0)); d <= 0 {
		t.Fatalf("subtract should carve b out of a: got %v", d)
	}
	inter := Intersect2(a, b)
	if d := inter.Distance(V2(0, 0)); !almostEq(d, 0, 1e-12) {
		t.Fatalf("intersection boundary: got %v want 0", d)
	}
	if d := inter.Distance(V2(2, 0)); d <= 0 {
		t.Fatalf("intersection must be empty at (2,0): got %v", d)
	}
}

func TestCSG3(t *testing.T) {
	s := Sphere{Center: V3(0, 0, 0), Radius: 1}
	b := Box3{Center: V3(0, 0, 0), Half: V3(0.5, 0.5, 0.5)}
	sub := Subtract3(s, b)
	// The box core must be carved out of the sphere.
	if d := sub.Distance(V3(0, 0, 0)); d <= 0 {
		t.Fatalf("subtract3: center should be outside, got %v", d)
	}
	// A point on the sphere equator away from the box stays inside.
	if d := sub.Distance(V3(0, 0, 0.9)); d >= 0 {
		t.Fatalf("subtract3: (0,0,0.9) should be inside, got %v", d)
	}
}

// TestSample2Acceptance is the 2D acceptance boundary: rasterize a
// geometry model into a 64x64 signed distance field grid.
func TestSample2Acceptance(t *testing.T) {
	scene := Union2(
		Circle{Center: V2(0, 0), Radius: 0.6},
		Box2{Center: V2(0.7, 0), Half: V2(0.3, 0.3)},
	)
	g := Sample2(scene, 64, 64, V2(-1.5, -1.5), V2(1.5, 1.5))
	if len(g.Values) != 64*64 {
		t.Fatalf("grid size: got %d want %d", len(g.Values), 64*64)
	}
	// Center of the disc must be inside (negative).
	if v := g.At(32, 32); v >= 0 {
		t.Fatalf("center value should be negative, got %v", v)
	}
	// All four corners are far outside (positive).
	for _, ij := range [][2]int{{0, 0}, {63, 0}, {0, 63}, {63, 63}} {
		if v := g.At(ij[0], ij[1]); v <= 0 {
			t.Fatalf("corner %v should be positive, got %v", ij, v)
		}
	}
	// The scene is symmetric about y=0: rows j and 63-j must match.
	for j := 0; j < 64; j++ {
		for i := 0; i < 64; i++ {
			if a, b := g.At(i, j), g.At(i, 63-j); !almostEq(a, b, 1e-9) {
				t.Fatalf("asymmetry at (%d,%d): %v vs %v", i, j, a, b)
			}
		}
	}
}

// TestSample3Acceptance is the 3D counterpart: a 64x64x64 grid.
func TestSample3Acceptance(t *testing.T) {
	scene := Sphere{Center: V3(0, 0, 0), Radius: 1}
	g := Sample3(scene, 64, 64, 64, V3(-1.5, -1.5, -1.5), V3(1.5, 1.5, 1.5))
	if len(g.Values) != 64*64*64 {
		t.Fatalf("grid size: got %d", len(g.Values))
	}
	if v := g.At(32, 32, 32); v >= 0 {
		t.Fatalf("center should be inside, got %v", v)
	}
	if v := g.At(0, 0, 0); v <= 0 {
		t.Fatalf("corner should be outside, got %v", v)
	}
	// Trilinear interpolation must reproduce node values exactly.
	p := g.Position(10, 20, 30)
	if got, want := g.Value(p), g.At(10, 20, 30); !almostEq(got, want, 1e-12) {
		t.Fatalf("trilinear at node: got %v want %v", got, want)
	}
}

func TestGrid2BilinearAtNodes(t *testing.T) {
	c := Circle{Center: V2(0.1, -0.2), Radius: 0.5}
	g := Sample2(c, 64, 64, V2(-1, -1), V2(1, 1))
	for j := 0; j < 64; j += 7 {
		for i := 0; i < 64; i += 5 {
			p := g.Position(i, j)
			if got, want := g.Value(p), g.At(i, j); !almostEq(got, want, 1e-12) {
				t.Fatalf("bilinear at (%d,%d): got %v want %v", i, j, got, want)
			}
		}
	}
}

// TestGridApproximatesAnalytic checks that the rasterized 64x64 field
// approximates the analytic SDF within interpolation error O(h^2).
func TestGridApproximatesAnalytic(t *testing.T) {
	c := Circle{Center: V2(0, 0), Radius: 0.7}
	g := Sample2(c, 64, 64, V2(-1.5, -1.5), V2(1.5, 1.5))
	h := 3.0 / 63
	maxErr := 0.0
	for n := 0; n < 2000; n++ {
		p := V2(-1.4+2.8*float64(n%40)/39, -1.4+2.8*float64(n/40)/49)
		err := math.Abs(g.Value(p) - c.Distance(p))
		if err > maxErr {
			maxErr = err
		}
	}
	if maxErr > 2*h*h {
		t.Fatalf("interpolation error %v exceeds 2h^2=%v", maxErr, 2*h*h)
	}
}
