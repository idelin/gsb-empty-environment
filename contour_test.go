package contour

import (
	"math/rand"
	"testing"
)

func mustValidate(t *testing.T, im *Image, res *Result) {
	t.Helper()
	if err := Validate(im, res); err != nil {
		t.Fatalf("Validate failed:\n%v\nimage:\n%s", err, im)
	}
}

func TestSinglePixel(t *testing.T) {
	im := ImageFromASCII("#")
	res := Extract(im)
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	r := res.Regions[0]
	if r.Area != 1 || r.Perimeter != 4 || r.Holes != 0 {
		t.Fatalf("got area=%d perim=%d holes=%d, want 1/4/0", r.Area, r.Perimeter, r.Holes)
	}
	if len(res.Loops) != 1 || res.Loops[0].Len() != 4 || res.Loops[0].Hole {
		t.Fatalf("want one outer 4-edge loop, got %+v", res.Loops)
	}
	mustValidate(t, im, res)
}

func TestSinglePixelHole(t *testing.T) {
	im := ImageFromASCII(
		"###",
		"#.#",
		"###",
	)
	res := Extract(im)
	r := res.Regions[0]
	if len(res.Regions) != 1 || r.Area != 8 || r.Perimeter != 16 || r.Holes != 1 {
		t.Fatalf("got %+v regions=%d", r, len(res.Regions))
	}
	if len(res.Loops) != 2 {
		t.Fatalf("want outer+hole loop, got %d", len(res.Loops))
	}
	mustValidate(t, im, res)
}

// A diagonal background slit reaches the outside under 8-connectivity,
// so it is not a hole; the outer loop self-touches at the pinch vertex.
func TestDiagonalSlit(t *testing.T) {
	im := ImageFromASCII(
		".##",
		"#.#",
		"###",
	)
	res := Extract(im)
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1 (foreground stays 4-connected)", len(res.Regions))
	}
	r := res.Regions[0]
	if r.Holes != 0 {
		t.Fatalf("holes = %d, want 0: diagonal slit leaks to the border", r.Holes)
	}
	if len(res.Loops) != 1 {
		t.Fatalf("loops = %d, want 1 self-touching outer loop", len(res.Loops))
	}
	if touches := res.Loops[0].SelfTouches(); len(touches) == 0 {
		t.Fatalf("outer loop should self-touch at the slit pinch")
	}
	mustValidate(t, im, res)
}

// Corner-touching foreground pixels are separate 4-connected regions,
// each with its own loop; the two loops share one grid vertex.
func TestCornerTouchRegionsSeparated(t *testing.T) {
	im := ImageFromASCII(
		"#.",
		".#",
	)
	res := Extract(im)
	if len(res.Regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(res.Regions))
	}
	for _, r := range res.Regions {
		if r.Area != 1 || r.Perimeter != 4 || r.Holes != 0 {
			t.Fatalf("region %+v, want area=1 perim=4 holes=0", r)
		}
	}
	if len(res.Loops) != 2 {
		t.Fatalf("loops = %d, want 2", len(res.Loops))
	}
	shared := map[Point]int{}
	for _, lp := range res.Loops {
		for _, p := range lp.Points {
			shared[p]++
		}
	}
	if shared[Point{1, 1}] != 2 { // each of the two loops passes the pinch vertex once
		t.Fatalf("loops should share the corner vertex (1,1), counts=%v", shared)
	}
	mustValidate(t, im, res)
}

// A diagonal background pair fully surrounded by foreground is one
// 8-connected hole whose loop self-touches at the pinch vertex.
func TestDiagonalHolePair(t *testing.T) {
	im := ImageFromASCII(
		"#####",
		"##.##",
		"#.###",
		"#####",
	)
	res := Extract(im)
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	r := res.Regions[0]
	if r.Holes != 1 {
		t.Fatalf("holes = %d, want 1 (diagonal background pair is 8-connected)", r.Holes)
	}
	var hole *Loop
	for i := range res.Loops {
		if res.Loops[i].Hole {
			hole = &res.Loops[i]
		}
	}
	if hole == nil || hole.Len() != 8 {
		t.Fatalf("want one 8-edge hole loop, got %+v", res.Loops)
	}
	if len(hole.SelfTouches()) != 1 {
		t.Fatalf("hole loop should self-touch at exactly one vertex, got %v", hole.SelfTouches())
	}
	mustValidate(t, im, res)
}

func TestEmptyAndFull(t *testing.T) {
	for _, im := range []*Image{NewImage(4, 3), NewImage(0, 0)} {
		res := Extract(im)
		if len(res.Regions) != 0 || len(res.Loops) != 0 {
			t.Fatalf("empty image: got %+v", res)
		}
		mustValidate(t, im, res)
	}
	full := NewImage(3, 2)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			full.Set(x, y, true)
		}
	}
	res := Extract(full)
	if len(res.Regions) != 1 || res.Regions[0].Perimeter != 10 || res.Regions[0].Holes != 0 {
		t.Fatalf("full image: got %+v", res.Regions)
	}
	mustValidate(t, full, res)
}

func TestReplayRoundTrip(t *testing.T) {
	im := ImageFromASCII(
		"##..###",
		"#.#..#.",
		"##.####",
		"..#.#..",
		".#####.",
	)
	res := Extract(im)
	back := Replay(res)
	if !back.equal(im) {
		t.Fatalf("replay mismatch:\nwant:\n%s\ngot:\n%s", im, back)
	}
	mustValidate(t, im, res)
}

// Fuzz every cross-check on random images: unit-edge perimeter counting,
// shoelace areas, flood-fill hole model, and replay round-trip.
func TestFuzzRandomImages(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		w := 1 + rng.Intn(12)
		h := 1 + rng.Intn(12)
		density := 0.25 + 0.5*rng.Float64()
		im := NewImage(w, h)
		for i := range im.Pix {
			im.Pix[i] = rng.Float64() < density
		}
		res := Extract(im)
		if err := Validate(im, res); err != nil {
			t.Fatalf("iter %d (%dx%d d=%.2f):\n%v\nimage:\n%s", iter, w, h, density, err, im)
		}
	}
}
