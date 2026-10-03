package delaunay

import (
	"math"
	"math/rand"
	"testing"
)

func randomPoints(n int, seed int64) []Point {
	r := rand.New(rand.NewSource(seed))
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{X: r.Float64() * 100, Y: r.Float64() * 100}
	}
	return pts
}

func dist2(a, b Point) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return dx*dx + dy*dy
}

// checkEmptyCircumcircle verifies the Empty Circumcircle Property: no input
// point may lie strictly inside the circumcircle of any triangle.
func checkEmptyCircumcircle(t *testing.T, m *Mesh) {
	t.Helper()
	for i, tr := range m.Triangles {
		a, b, c := m.Points[tr.V[0]], m.Points[tr.V[1]], m.Points[tr.V[2]]
		if orient2d(a, b, c) <= 0 {
			t.Errorf("triangle %d is not counter-clockwise", i)
		}
		center, r2 := Circumcircle(a, b, c)
		for j, p := range m.Points {
			if j == tr.V[0] || j == tr.V[1] || j == tr.V[2] {
				continue
			}
			if d2 := dist2(p, center); d2 < r2-1e-9*(r2+1) {
				t.Errorf("triangle %d: circumcircle contains point %d (d2=%g r2=%g)", i, j, d2, r2)
			}
		}
	}
}

// checkAdjacency verifies that neighbour references are symmetric and that
// adjacent triangles share exactly the two vertices of their common edge.
func checkAdjacency(t *testing.T, m *Mesh) {
	t.Helper()
	for i, tr := range m.Triangles {
		for k := 0; k < 3; k++ {
			nb := tr.N[k]
			if nb == -1 {
				continue
			}
			if nb < 0 || nb >= len(m.Triangles) {
				t.Fatalf("triangle %d: invalid neighbour index %d", i, nb)
			}
			back := false
			for l := 0; l < 3; l++ {
				if m.Triangles[nb].N[l] == i {
					back = true
				}
			}
			if !back {
				t.Errorf("triangle %d neighbour %d does not reference back", i, nb)
			}
			ea, eb := tr.V[(k+1)%3], tr.V[(k+2)%3]
			shared := 0
			for _, v := range m.Triangles[nb].V {
				if v == ea || v == eb {
					shared++
				}
			}
			if shared != 2 {
				t.Errorf("triangle %d and neighbour %d share %d vertices, want 2", i, nb, shared)
			}
		}
	}
}

// checkHull verifies the hull is convex, counter-clockwise, and contains
// every point of the set. With strict=true, collinear consecutive hull
// vertices are rejected.
func checkHull(t *testing.T, m *Mesh, strict bool) {
	t.Helper()
	h := m.Hull
	if len(h) < 3 {
		t.Fatalf("hull has %d vertices, want >= 3", len(h))
	}
	n := len(h)
	for i := range h {
		a := m.Points[h[i]]
		b := m.Points[h[(i+1)%n]]
		c := m.Points[h[(i+2)%n]]
		if o := orient2d(a, b, c); o < -1e-9 || (strict && o <= 1e-9) {
			t.Errorf("hull not convex/CCW at vertex %d (orient=%g)", h[(i+1)%n], o)
		}
		for j, p := range m.Points {
			if orient2d(a, b, p) < -1e-9 {
				t.Errorf("point %d lies outside hull edge (%d,%d)", j, h[i], h[(i+1)%n])
			}
		}
	}
}

// TestEmptyCircumcircle is the acceptance test: 50 random survey points must
// produce a triangulation where no triangle's circumcircle contains a fourth
// point, with consistent adjacency and a valid convex hull.
func TestEmptyCircumcircle(t *testing.T) {
	pts := randomPoints(50, 42)
	m := Triangulate(pts)
	if len(m.Triangles) == 0 {
		t.Fatal("no triangles produced")
	}
	checkEmptyCircumcircle(t, m)
	checkAdjacency(t, m)
	checkHull(t, m, true)
}

// TestEulerFormula checks the combinatorial identity T = 2N - 2 - H for a
// triangulation of N points with H hull vertices.
func TestEulerFormula(t *testing.T) {
	pts := randomPoints(50, 42)
	m := Triangulate(pts)
	want := 2*len(m.Points) - 2 - len(m.Hull)
	if len(m.Triangles) != want {
		t.Errorf("triangle count = %d, want 2N-2-H = %d", len(m.Triangles), want)
	}
}

// TestIncrementalInsertion inserts points one by one and re-validates the
// empty circumcircle property after every single insertion, exercising the
// dynamic insertion + Lawson flip path.
func TestIncrementalInsertion(t *testing.T) {
	pts := randomPoints(50, 7)
	tr := NewTriangulator(Point{0, 0}, Point{100, 100})
	for i, p := range pts {
		tr.Insert(p)
		if i < 2 {
			continue
		}
		m := tr.Mesh()
		checkEmptyCircumcircle(t, m)
		checkAdjacency(t, m)
		checkHull(t, m, false)
	}
}

// TestSquare checks a minimal known configuration.
func TestSquare(t *testing.T) {
	pts := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	m := Triangulate(pts)
	if len(m.Triangles) != 2 {
		t.Errorf("square: got %d triangles, want 2", len(m.Triangles))
	}
	if len(m.Hull) != 4 {
		t.Errorf("square: got %d hull vertices, want 4", len(m.Hull))
	}
	checkAdjacency(t, m)
	checkHull(t, m, true)
}

// TestPointOnEdge inserts a point exactly on an existing edge, exercising the
// split4 path.
func TestPointOnEdge(t *testing.T) {
	pts := []Point{{0, 0}, {4, 0}, {4, 4}, {0, 4}, {2, 0}}
	m := Triangulate(pts)
	// (2,0) is collinear on the hull edge (0,0)-(4,0), so the boundary
	// loop has 5 vertices and T = 2N-2-H = 3.
	if len(m.Hull) != 5 {
		t.Errorf("got %d hull vertices, want 5 (collinear point included)", len(m.Hull))
	}
	if got, want := len(m.Triangles), 2*5-2-5; got != want {
		t.Errorf("got %d triangles, want %d", got, want)
	}
	checkEmptyCircumcircle(t, m)
	checkAdjacency(t, m)
	checkHull(t, m, false)
}

// TestDuplicatePoints ensures duplicate insertions are ignored.
func TestDuplicatePoints(t *testing.T) {
	pts := randomPoints(20, 3)
	pts = append(pts, pts[0], pts[5], pts[5])
	m := Triangulate(pts)
	if len(m.Points) != 20 {
		t.Errorf("got %d points, want 20 (duplicates dropped)", len(m.Points))
	}
	checkEmptyCircumcircle(t, m)
	checkAdjacency(t, m)
}

// TestRandomSets sweeps several seeds and sizes for robustness.
func TestRandomSets(t *testing.T) {
	for _, tc := range []struct {
		n    int
		seed int64
	}{{10, 1}, {30, 2}, {50, 42}, {100, 99}, {200, 7}} {
		m := Triangulate(randomPoints(tc.n, tc.seed))
		checkEmptyCircumcircle(t, m)
		checkAdjacency(t, m)
		checkHull(t, m, true)
		if want := 2*tc.n - 2 - len(m.Hull); len(m.Triangles) != want {
			t.Errorf("n=%d: got %d triangles, want %d", tc.n, len(m.Triangles), want)
		}
	}
}

// TestAngleQuality reports (and sanity-checks) the minimum angle, a proxy for
// the smoothness/uniformity of the mesh.
func TestAngleQuality(t *testing.T) {
	m := Triangulate(randomPoints(50, 42))
	minAngle := math.Inf(1)
	for _, tr := range m.Triangles {
		p := [3]Point{m.Points[tr.V[0]], m.Points[tr.V[1]], m.Points[tr.V[2]]}
		for i := 0; i < 3; i++ {
			a := dist2(p[(i+1)%3], p[(i+2)%3])
			b := dist2(p[i], p[(i+1)%3])
			c := dist2(p[i], p[(i+2)%3])
			cos := (b + c - a) / (2 * math.Sqrt(b*c))
			ang := math.Acos(math.Min(1, math.Max(-1, cos))) * 180 / math.Pi
			if ang < minAngle {
				minAngle = ang
			}
		}
	}
	t.Logf("minimum triangle angle: %.2f deg", minAngle)
	if minAngle <= 0 {
		t.Errorf("degenerate triangle detected (min angle %g)", minAngle)
	}
}
