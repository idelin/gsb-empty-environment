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
		pts[i] = Point{X: 100 * r.Float64(), Y: 100 * r.Float64()}
	}
	return pts
}

// checkTopology verifies that every triangle is counter-clockwise and that
// neighbour references are mutual and share exactly the opposite edge.
func checkTopology(t *testing.T, tri *Triangulation) {
	t.Helper()
	for ti, tr := range tri.Tris {
		a, b, c := tri.Points[tr.V[0]], tri.Points[tr.V[1]], tri.Points[tr.V[2]]
		if orient(a, b, c) <= 0 {
			t.Errorf("triangle %d is not counter-clockwise: %v", ti, tr.V)
		}
		for j := 0; j < 3; j++ {
			n := tr.N[j]
			if n < 0 {
				continue // hull boundary edge
			}
			if n >= len(tri.Tris) {
				t.Fatalf("triangle %d: neighbour index %d out of range", ti, n)
			}
			e1, e2 := tr.V[(j+1)%3], tr.V[(j+2)%3]
			nt := tri.Tris[n]
			found := false
			for k := 0; k < 3; k++ {
				if nt.N[k] != ti {
					continue
				}
				x, y := nt.V[(k+1)%3], nt.V[(k+2)%3]
				if (x == e1 && y == e2) || (x == e2 && y == e1) {
					found = true
				}
			}
			if !found {
				t.Errorf("triangle %d edge %d: neighbour %d does not reference back across the shared edge", ti, j, n)
			}
		}
	}
}

// checkHull verifies that the hull is a closed counter-clockwise loop of
// boundary edges containing every point of the mesh.
func checkHull(t *testing.T, tri *Triangulation) {
	t.Helper()
	hull := tri.ConvexHull()
	if len(hull) < 3 {
		t.Fatalf("convex hull has %d vertices, want at least 3", len(hull))
	}
	boundary := 0
	for _, tr := range tri.Tris {
		for j := 0; j < 3; j++ {
			if tr.N[j] < 0 {
				boundary++
			}
		}
	}
	if boundary != len(hull) {
		t.Errorf("hull has %d vertices but mesh has %d boundary edges", len(hull), boundary)
	}
	area := 0.0
	for i, v := range hull {
		p, q := tri.Points[v], tri.Points[hull[(i+1)%len(hull)]]
		area += p.X*q.Y - p.Y*q.X
	}
	if area <= 0 {
		t.Errorf("hull is not counter-clockwise (signed area %v)", area/2)
	}
	for i, v := range hull {
		a, b := tri.Points[v], tri.Points[hull[(i+1)%len(hull)]]
		for j, p := range tri.Points {
			if orient(a, b, p) < -1e-9 {
				t.Errorf("point %d lies outside hull edge %d", j, i)
			}
		}
	}
}

func TestEmptyCircumcircleRandom50(t *testing.T) {
	pts := randomPoints(50, 42)
	tri := NewBuilder(pts...).Triangulation()

	if len(tri.Points) != 50 {
		t.Fatalf("got %d points, want 50", len(tri.Points))
	}
	if !tri.IsDelaunay() {
		t.Error("empty circumcircle property violated")
	}
	checkTopology(t, tri)
	checkHull(t, tri)

	hull := tri.ConvexHull()
	if want := 2*len(tri.Points) - 2 - len(hull); len(tri.Tris) != want {
		t.Errorf("got %d triangles, want %d (Euler: 2n-2-h)", len(tri.Tris), want)
	}
}

func TestIncrementalInsertionStaysDelaunay(t *testing.T) {
	pts := randomPoints(50, 7)
	b := NewBuilder()
	for i, p := range pts {
		b.Insert(p)
		if tri := b.Triangulation(); !tri.IsDelaunay() {
			t.Fatalf("empty circumcircle property violated after inserting point %d", i)
		}
	}
	tri := b.Triangulation()
	checkTopology(t, tri)
	checkHull(t, tri)
}

func TestGridPointsOnEdges(t *testing.T) {
	// Integer grid: many inserted points land exactly on existing edges and
	// many quadruples are co-circular, exercising splitEdge and degenerate
	// in-circle tests.
	const n = 8
	var pts []Point
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			pts = append(pts, Point{X: float64(i), Y: float64(j)})
		}
	}
	tri := NewBuilder(pts...).Triangulation()
	if !tri.IsDelaunay() {
		t.Error("empty circumcircle property violated on grid")
	}
	checkTopology(t, tri)
	checkHull(t, tri)
	if want := 2 * (n - 1) * (n - 1); len(tri.Tris) != want {
		t.Errorf("got %d triangles, want %d", len(tri.Tris), want)
	}
	if hull := tri.ConvexHull(); len(hull) != 4*(n-1) {
		t.Errorf("hull has %d vertices, want %d", len(hull), 4*(n-1))
	}
}

func TestDuplicatePoints(t *testing.T) {
	b := NewBuilder()
	b.Insert(Point{0, 0})
	b.Insert(Point{4, 0})
	b.Insert(Point{0, 3})
	if got := b.Insert(Point{0, 0}); got != 0 {
		t.Errorf("duplicate insert returned index %d, want 0", got)
	}
	tri := b.Triangulation()
	if len(tri.Points) != 3 || len(tri.Tris) != 1 {
		t.Errorf("got %d points / %d triangles, want 3 / 1", len(tri.Points), len(tri.Tris))
	}
}

func TestCircumcircle(t *testing.T) {
	center, r2 := Circumcircle(Point{0, 0}, Point{4, 0}, Point{0, 3})
	if math.Abs(center.X-2) > 1e-12 || math.Abs(center.Y-1.5) > 1e-12 {
		t.Errorf("center = %v, want (2, 1.5)", center)
	}
	if math.Abs(r2-6.25) > 1e-12 {
		t.Errorf("r^2 = %v, want 6.25", r2)
	}
}

func TestManyRandomSeeds(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		tri := NewBuilder(randomPoints(50, seed)...).Triangulation()
		if !tri.IsDelaunay() {
			t.Errorf("seed %d: empty circumcircle property violated", seed)
		}
		checkTopology(t, tri)
		checkHull(t, tri)
	}
}
