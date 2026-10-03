package sdf

import (
	"testing"
)

func TestCubeMeshDistance(t *testing.T) {
	m := CubeMesh(V3(0, 0, 0), 1)
	cases := []struct {
		p    Vec3
		want float64
	}{
		{V3(3, 0, 0), 2},       // outside, facing +x face
		{V3(0, 0, 0), -1},      // center, deepest inside
		{V3(1.5, 0.5, 0), 0.5}, // outside near face
		{V3(0.5, 0, 0), -0.5},  // inside
		{V3(1, 0, 0), 0},       // exactly on the surface
	}
	for _, c := range cases {
		if d := m.Distance(c.p); !almostEq(d, c.want, 1e-9) {
			t.Fatalf("distance at %v: got %v want %v", c.p, d, c.want)
		}
	}
}

func TestMeshSignConvention(t *testing.T) {
	m := CubeMesh(V3(0, 0, 0), 1)
	// Sample a shell of directions just inside and just outside.
	for i := 0; i < 8; i++ {
		dir := V3(
			float64(i&1)*2-1,
			float64((i>>1)&1)*2-1,
			float64((i>>2)&1)*2-1,
		).Normalize()
		if d := m.Distance(dir.Scale(0.5)); d >= 0 {
			t.Fatalf("inside point %v should be negative, got %v", dir, d)
		}
		if d := m.Distance(dir.Scale(2)); d <= 0 {
			t.Fatalf("outside point %v should be positive, got %v", dir, d)
		}
	}
}

// TestRayMarchMesh: the mesh SDF is a true distance field, so sphere
// tracing converges on it directly.
func TestRayMarchMesh(t *testing.T) {
	m := CubeMesh(V3(0, 0, 0), 1)
	res := RayMarch3(m, V3(5, 0.3, -0.2), V3(-1, 0, 0), MarchOptions{})
	if !res.Hit {
		t.Fatal("expected hit on cube mesh")
	}
	if !almostEq(res.T, 4, 1e-3) {
		t.Fatalf("hit distance: got %v want ~4", res.T)
	}
	if !almostEq(res.Position.X, 1, 1e-3) {
		t.Fatalf("hit x: got %v want ~1", res.Position.X)
	}
}

// TestMeshRasterizedRoundTrip: rasterize a mesh into a grid, then
// extract the iso-surface back out of the grid.
func TestMeshRasterizedRoundTrip(t *testing.T) {
	m := CubeMesh(V3(0, 0, 0), 0.8)
	g := Sample3(m, 32, 32, 32, V3(-1.5, -1.5, -1.5), V3(1.5, 1.5, 1.5))
	tris := g.Surface(0)
	if len(tris) == 0 {
		t.Fatal("no surface extracted from rasterized mesh")
	}
	h := 3.0 / 31
	for _, tri := range tris {
		for _, p := range [3]Vec3{tri.A, tri.B, tri.C} {
			if d := m.Distance(p); d < -h || d > h {
				t.Fatalf("extracted vertex %v has mesh distance %v", p, d)
			}
		}
	}
}
