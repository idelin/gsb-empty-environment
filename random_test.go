package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

func TestRandomCubeCutsRemainManifold(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	base := unitCube()
	for i := 0; i < 300; i++ {
		n := Vec3{rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()}
		if n.Dot(n) < 1e-6 {
			i--
			continue
		}
		q := Vec3{rng.Float64(), rng.Float64(), rng.Float64()}
		plane, err := PlaneThrough(n, q)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Cut(base, plane)
		if err != nil {
			t.Fatalf("case %d plane=%+v q=%v: %v", i, plane, q, err)
		}
		if len(res.Mesh.Triangles) == 0 {
			continue
		}
		if err := res.Mesh.Validate(); err != nil {
			t.Fatalf("case %d plane=%+v q=%v: %v", i, plane, q, err)
		}
		vol, err := SignedVolume(res.Mesh)
		if err != nil {
			t.Fatal(err)
		}
		if vol < -1e-8 || vol > 1+1e-8 {
			t.Fatalf("case %d volume=%v plane=%+v q=%v", i, vol, plane, q)
		}
		for _, idx := range res.CapIndices {
			tri := res.Triangles[idx]
			a, b, c := res.Vertices[tri[0]], res.Vertices[tri[1]], res.Vertices[tri[2]]
			if b.Sub(a).Cross(c.Sub(a)).Dot(plane.Normal()) >= 0 {
				t.Fatalf("case %d cap normal wrong plane=%+v", i, plane)
			}
		}
		for _, l := range res.Loops {
			if math.Abs(l.Area) < 1e-12 {
				t.Fatalf("case %d zero-area loop", i)
			}
			if l.Depth%2 == 0 && l.Area <= 0 {
				t.Fatalf("case %d outer loop has non-positive area %v", i, l.Area)
			}
			if l.Depth%2 == 1 && l.Area >= 0 {
				t.Fatalf("case %d hole loop has non-negative area %v", i, l.Area)
			}
		}
	}
}

func TestRandomPrismCutsRemainManifold(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	base := twoHolePrism()
	for i := 0; i < 80; i++ {
		n := Vec3{rng.Float64()*0.6 - 0.3, rng.Float64()*0.6 - 0.3, 1}
		q := Vec3{1 + rng.Float64()*3, 0.5 + rng.Float64()*2, 1}
		plane, err := PlaneThrough(n, q)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Cut(base, plane)
		if err != nil {
			t.Fatalf("case %d plane=%+v q=%v: %v", i, plane, q, err)
		}
		if len(res.Mesh.Triangles) == 0 {
			continue
		}
		if err := res.Mesh.Validate(); err != nil {
			t.Fatalf("case %d plane=%+v q=%v: %v", i, plane, q, err)
		}
		vol, err := SignedVolume(res.Mesh)
		if err != nil {
			t.Fatal(err)
		}
		if vol <= 0 || vol > 30 {
			t.Fatalf("case %d volume=%v plane=%+v q=%v", i, vol, plane, q)
		}
	}
}
