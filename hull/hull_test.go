package hull

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSquareWithEdgePointsAndDuplicates(t *testing.T) {
	pts := []Point{
		Pt(0, 0), Pt(4, 0), Pt(4, 4), Pt(0, 4), // corners
		Pt(2, 0), Pt(4, 2), Pt(2, 4), Pt(0, 2), // edge midpoints
		Pt(2, 2), Pt(1, 1), // interior
		Pt(0, 0), Pt(4, 4), Pt(2, 0), // duplicates
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(h.Vertices) != 4 {
		t.Fatalf("want 4 vertices, got %d: %v", len(h.Vertices), h.Vertices)
	}
	// Bottom edge (0,0)->(4,0) must carry ids 0,1,4,10,12.
	if got := h.Edges[0].Collinear; fmt.Sprint(got) != "[0 1 4 10 12]" {
		t.Fatalf("bottom edge collinear ids = %v", got)
	}
}

func TestAllCollinearKeepsTwoEndpoints(t *testing.T) {
	pts := []Point{
		Pt(2, 2), Pt(0, 0), Pt(5, 5), Pt(1, 1), Pt(3, 3), Pt(5, 5), Pt(0, 0),
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(h.Vertices) != 2 {
		t.Fatalf("want 2 vertices, got %v", h.Vertices)
	}
	if pts[h.Vertices[0]].X.Cmp(Pt(0, 0).X) != 0 || pts[h.Vertices[1]].X.Cmp(Pt(5, 5).X) != 0 {
		t.Fatalf("endpoints = %v, %v", pts[h.Vertices[0]], pts[h.Vertices[1]])
	}
	if len(h.Edges) != 2 {
		t.Fatalf("want 2 directed edges, got %d", len(h.Edges))
	}
	for _, e := range h.Edges {
		if len(e.Collinear) != len(pts) {
			t.Fatalf("edge (%d,%d) collinear = %v, want all %d ids",
				e.From, e.To, e.Collinear, len(pts))
		}
	}
}

func TestRationalCoordinates(t *testing.T) {
	pts := []Point{
		Rat("0", "0"), Rat("1/3", "0"), Rat("2/3", "0"), Rat("1", "0"),
		Rat("1", "1/2"), Rat("1/2", "7/8"), Rat("0", "1/2"),
		Rat("1/2", "1/4"),                  // interior
		Rat("1/3", "0"), Rat("1/2", "7/8"), // duplicates
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(h.Vertices) != 5 {
		t.Fatalf("want 5 vertices, got %v", h.Vertices)
	}
	// Bottom edge carries the four collinear ids on y=0.
	if got := h.Edges[0].Collinear; fmt.Sprint(got) != "[0 1 2 3 8]" {
		t.Fatalf("bottom edge collinear ids = %v", got)
	}
}

func TestDegenerateClouds(t *testing.T) {
	if h := ConvexHull(nil); len(h.Vertices) != 0 || len(h.Edges) != 0 {
		t.Fatalf("empty cloud: %+v", h)
	}
	if err := Verify(nil, ConvexHull(nil)); err != nil {
		t.Fatalf("verify empty: %v", err)
	}
	one := []Point{Pt(3, 4), Pt(3, 4), Pt(3, 4)}
	h := ConvexHull(one)
	if err := Verify(one, h); err != nil {
		t.Fatalf("verify single: %v", err)
	}
	if len(h.Vertices) != 1 || h.Vertices[0] != 0 {
		t.Fatalf("single-point hull: %+v", h)
	}
}

func TestFloatConversionIsExact(t *testing.T) {
	p := Float(0.1, 0.2)
	// 0.1 and 0.2 are not exactly representable; the exact binary value
	// must be preserved, not rounded to 1/10 or 1/5.
	if p.X.RatString() != "3602879701896397/36028797018963968" ||
		p.Y.RatString() != "3602879701896397/18014398509481984" {
		t.Fatalf("0.1/0.2 not converted exactly: %v %v", p.X, p.Y)
	}
}

// TestRandomClouds cross-checks ConvexHull against the exhaustive
// supporting-line verifier on random rational clouds with duplicates.
func TestRandomClouds(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(30)
		pts := make([]Point, n)
		for i := range pts {
			num := int64(rng.Intn(21) - 10)
			den := int64(1 + rng.Intn(4))
			x := Rat(fmt.Sprintf("%d/%d", num, den), "0")
			num = int64(rng.Intn(21) - 10)
			den = int64(1 + rng.Intn(4))
			y := Rat(fmt.Sprintf("%d/%d", num, den), "0")
			pts[i] = Point{x.X, y.Y}
			// Inject duplicates and collinear grid points.
			if i > 0 && rng.Intn(4) == 0 {
				pts[i] = pts[rng.Intn(i)]
			}
		}
		h := ConvexHull(pts)
		if err := Verify(pts, h); err != nil {
			t.Fatalf("trial %d: %v\npts=%v", trial, err, pts)
		}
	}
}

// TestCollinearRandom checks degenerate (fully collinear) random clouds.
func TestCollinearRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		n := 2 + rng.Intn(15)
		pts := make([]Point, n)
		for i := range pts {
			k := int64(rng.Intn(11) - 5)
			pts[i] = Rat(fmt.Sprintf("%d/3", k), fmt.Sprintf("%d/2", 2*k))
		}
		h := ConvexHull(pts)
		if err := Verify(pts, h); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		allSame := true
		for _, p := range pts[1:] {
			if key(p) != key(pts[0]) {
				allSame = false
				break
			}
		}
		want := 2
		if allSame {
			want = 1
		}
		if len(h.Vertices) != want {
			t.Fatalf("trial %d: want %d vertices, got %d", trial, want, len(h.Vertices))
		}
	}
}
