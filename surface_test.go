package voxelsurf

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func cube(side int64) []Point {
	var pts []Point
	for x := int64(0); x < side; x++ {
		for y := int64(0); y < side; y++ {
			for z := int64(0); z < side; z++ {
				pts = append(pts, Point{x, y, z})
			}
		}
	}
	return pts
}

// shell returns the surface voxels of a side^3 cube (a sealed hollow box for
// side >= 3).
func shell(side int64) []Point {
	var pts []Point
	for _, p := range cube(side) {
		if p.X == 0 || p.Y == 0 || p.Z == 0 ||
			p.X == side-1 || p.Y == side-1 || p.Z == side-1 {
			pts = append(pts, p)
		}
	}
	return pts
}

func translate(pts []Point, d Point) []Point {
	out := make([]Point, len(pts))
	for i, p := range pts {
		out[i] = Point{p.X + d.X, p.Y + d.Y, p.Z + d.Z}
	}
	return out
}

func shiftFace(f Face, d Point) Face {
	switch f.Axis {
	case 0:
		return Face{f.Axis, f.Plane + d.X, f.U + d.Y, f.V + d.Z}
	case 1:
		return Face{f.Axis, f.Plane + d.Y, f.U + d.X, f.V + d.Z}
	default:
		return Face{f.Axis, f.Plane + d.Z, f.U + d.X, f.V + d.Y}
	}
}

func TestAnalyzeBasic(t *testing.T) {
	dup := []Point{{5, -3, 7}, {5, -3, 7}, {5, -3, 7}}
	cases := []struct {
		name                     string
		pts                      []Point
		voxels                   int
		total, external          int
		cavityArea, cavityVolume int
		cavities                 int
	}{
		{"empty", nil, 0, 0, 0, 0, 0, 0},
		{"single", []Point{{5, -3, 7}}, 1, 6, 6, 0, 0, 0},
		{"duplicates", dup, 1, 6, 6, 0, 0, 0},
		{"pair", []Point{{0, 0, 0}, {1, 0, 0}}, 2, 10, 10, 0, 0, 0},
		{"far-apart", []Point{{0, 0, 0}, {1_000_000_000, -1_000_000_000, 5}}, 2, 12, 12, 0, 0, 0},
		{"cube2", cube(2), 8, 24, 24, 0, 0, 0},
		{"cube3", cube(3), 27, 54, 54, 0, 0, 0},
		{"shell3", shell(3), 26, 60, 54, 6, 1, 1},
		{"shell5", shell(5), 98, 204, 150, 54, 27, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Analyze(tc.pts)
			if a.Voxels != tc.voxels || a.TotalArea != tc.total || a.ExternalArea != tc.external ||
				a.CavityArea != tc.cavityArea || a.CavityVolume != tc.cavityVolume || len(a.Cavities) != tc.cavities {
				t.Fatalf("got %s, want voxels=%d total=%d external=%d cavityArea=%d cavityVolume=%d cavities=%d",
					a, tc.voxels, tc.total, tc.external, tc.cavityArea, tc.cavityVolume, tc.cavities)
			}
			if a.TotalArea != a.ExternalArea+a.CavityArea {
				t.Fatalf("area mismatch: %s", a)
			}
			if len(a.ExternalFaces) != a.ExternalArea {
				t.Fatalf("external face evidence mismatch: %d faces for area %d", len(a.ExternalFaces), a.ExternalArea)
			}
			sum := 0
			for _, c := range a.Cavities {
				sum += c.Volume
			}
			if sum != a.CavityVolume {
				t.Fatalf("cavity volume evidence mismatch: %d != %d", sum, a.CavityVolume)
			}
		})
	}
}

// TestCavityFaceEvidence checks the exact face numbers sealing the unit
// cavity inside a 3x3x3 shell.
func TestCavityFaceEvidence(t *testing.T) {
	a := Analyze(shell(3))
	if len(a.Cavities) != 1 {
		t.Fatalf("want 1 cavity, got %d", len(a.Cavities))
	}
	want := []Face{
		{Axis: 0, Plane: 1, U: 1, V: 1}, // between (0,1,1) and (1,1,1)
		{Axis: 0, Plane: 2, U: 1, V: 1}, // between (1,1,1) and (2,1,1)
		{Axis: 1, Plane: 1, U: 1, V: 1},
		{Axis: 1, Plane: 2, U: 1, V: 1},
		{Axis: 2, Plane: 1, U: 1, V: 1},
		{Axis: 2, Plane: 2, U: 1, V: 1},
	}
	if !reflect.DeepEqual(a.Cavities[0].Faces, want) {
		t.Fatalf("cavity faces = %v, want %v", a.Cavities[0].Faces, want)
	}
}

// TestTranslationInvariance verifies that huge coordinate offsets do not
// change the result (no bounding-box allocation, compression is exact).
func TestTranslationInvariance(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	base := make([]Point, 0, 200)
	seen := map[Point]bool{}
	for len(base) < 150 {
		p := Point{int64(rng.Intn(9)), int64(rng.Intn(9)), int64(rng.Intn(9))}
		if !seen[p] {
			seen[p] = true
			base = append(base, p)
		}
	}
	off := Point{1_000_000_000, -2_000_000_000, 3_000_000_000}
	a := Analyze(base)
	b := Analyze(translate(base, off))

	if a.TotalArea != b.TotalArea || a.ExternalArea != b.ExternalArea ||
		a.CavityVolume != b.CavityVolume || a.CavityArea != b.CavityArea {
		t.Fatalf("areas differ after translation: %s vs %s", a, b)
	}
	shifted := make([]Face, len(a.ExternalFaces))
	for i, f := range a.ExternalFaces {
		shifted[i] = shiftFace(f, off)
	}
	sortFaces(shifted)
	if !reflect.DeepEqual(shifted, b.ExternalFaces) {
		t.Fatalf("external face evidence differs after translation")
	}
	if len(a.Cavities) != len(b.Cavities) {
		t.Fatalf("cavity count differs: %d vs %d", len(a.Cavities), len(b.Cavities))
	}
	for i := range a.Cavities {
		if a.Cavities[i].Volume != b.Cavities[i].Volume {
			t.Fatalf("cavity %d volume differs: %d vs %d", i, a.Cavities[i].Volume, b.Cavities[i].Volume)
		}
	}
}

// TestCrossCheckRandom compares the coordinate-compression flood fill against
// the naive per-cell dense model on many random small voxel sets.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		side := int64(2 + rng.Intn(8)) // 2..9
		count := rng.Intn(120)
		pts := make([]Point, 0, count)
		for i := 0; i < count; i++ {
			pts = append(pts, Point{rng.Int63n(side), rng.Int63n(side), rng.Int63n(side)})
		}
		fast := Analyze(pts)
		slow := AnalyzeDense(pts)
		if !reflect.DeepEqual(fast, slow) {
			t.Fatalf("iter %d mismatch:\nfast: %s\nslow: %s\nfast faces: %v\nslow faces: %v",
				iter, fast, slow, fast.ExternalFaces, slow.ExternalFaces)
		}
	}
}

// TestCrossCheckStructured adds non-random shapes with known cavities.
func TestCrossCheckStructured(t *testing.T) {
	shapes := [][]Point{
		shell(3), shell(4), shell(5), shell(6),
		append(shell(5), translate(shell(3), Point{10, 0, 0})...), // two cavities
		cube(4),
	}
	for i, pts := range shapes {
		fast := Analyze(pts)
		slow := AnalyzeDense(pts)
		if !reflect.DeepEqual(fast, slow) {
			t.Fatalf("shape %d mismatch:\nfast: %s\nslow: %s", i, fast, slow)
		}
	}
}

// TestLargeSpanSparse uses 10000 voxels scattered over a +/-1e9 range: the
// bounding box would be impossible to allocate, compression must handle it.
func TestLargeSpanSparse(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 10000
	pts := make([]Point, 0, n)
	set := map[Point]bool{}
	for len(pts) < n {
		p := Point{rng.Int63n(2_000_000_000) - 1_000_000_000,
			rng.Int63n(2_000_000_000) - 1_000_000_000,
			rng.Int63n(2_000_000_000) - 1_000_000_000}
		if !set[p] {
			set[p] = true
			pts = append(pts, p)
		}
	}
	// Independent total-area computation: 6n - 2 * adjacent pairs.
	adj := 0
	for p := range set {
		if set[Point{p.X + 1, p.Y, p.Z}] {
			adj++
		}
		if set[Point{p.X, p.Y + 1, p.Z}] {
			adj++
		}
		if set[Point{p.X, p.Y, p.Z + 1}] {
			adj++
		}
	}
	wantTotal := 6*n - 2*adj

	a := Analyze(pts)
	if a.TotalArea != wantTotal {
		t.Fatalf("total area = %d, want %d", a.TotalArea, wantTotal)
	}
	if a.ExternalArea != a.TotalArea || a.CavityVolume != 0 {
		t.Fatalf("sparse set should have no cavities: %s", a)
	}
}

func ExampleAnalyze() {
	a := Analyze(shell(3))
	fmt.Println(a)
	// Output:
	// voxels=26 total=60 external=54 cavityArea=6 cavityVolume=1 cavities=1
}
