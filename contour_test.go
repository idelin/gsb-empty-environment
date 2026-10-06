package contour

import (
	"reflect"
	"testing"
)

func mustValid(t *testing.T, res *Result) {
	t.Helper()
	for _, p := range res.Validate() {
		t.Errorf("validation: %s", p)
	}
}

func loopKinds(res *Result) (outer, holes int) {
	for _, l := range res.Loops {
		if l.Kind == Hole {
			holes++
		} else {
			outer++
		}
	}
	return
}

func TestEmpty(t *testing.T) {
	res := Analyze(FromRows("...", "..."))
	if len(res.Regions) != 0 || len(res.Loops) != 0 {
		t.Fatalf("expected no regions or loops, got %d regions, %d loops", len(res.Regions), len(res.Loops))
	}
	mustValid(t, res)
}

func TestSingleCell(t *testing.T) {
	res := Analyze(FromRows("#"))
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	reg := res.Regions[0]
	if reg.Area != 1 || reg.Perimeter != 4 || reg.Holes != 0 {
		t.Fatalf("area=%d perimeter=%d holes=%d, want 1/4/0", reg.Area, reg.Perimeter, reg.Holes)
	}
	if len(res.Loops) != 1 || res.Loops[0].Len() != 4 || res.Loops[0].Kind != Outer {
		t.Fatalf("loops = %+v, want one outer loop of length 4", res.Loops)
	}
	mustValid(t, res)
}

// Corner-touching (diagonal) foreground cells must stay separate regions.
func TestCornerTouchingRegionsSeparate(t *testing.T) {
	res := Analyze(FromRows(
		"#.",
		".#",
	))
	if len(res.Regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(res.Regions))
	}
	for _, reg := range res.Regions {
		if reg.Area != 1 || reg.Perimeter != 4 || reg.Holes != 0 {
			t.Fatalf("region %d: area=%d perimeter=%d holes=%d, want 1/4/0", reg.ID, reg.Area, reg.Perimeter, reg.Holes)
		}
	}
	if len(res.Loops) != 2 {
		t.Fatalf("loops = %d, want 2", len(res.Loops))
	}
	for _, l := range res.Loops {
		if l.Kind != Outer || l.Len() != 4 {
			t.Fatalf("loop of region %d: kind=%v len=%d, want outer/4", l.Region, l.Kind, l.Len())
		}
	}
	mustValid(t, res)
}

// A 3x3 ring encloses a single-pixel hole.
func TestSinglePixelHole(t *testing.T) {
	res := Analyze(FromRows(
		"###",
		"#.#",
		"###",
	))
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	reg := res.Regions[0]
	if reg.Area != 8 || reg.Perimeter != 16 || reg.Holes != 1 {
		t.Fatalf("area=%d perimeter=%d holes=%d, want 8/16/1", reg.Area, reg.Perimeter, reg.Holes)
	}
	outer, holes := loopKinds(res)
	if outer != 1 || holes != 1 {
		t.Fatalf("outer=%d holes=%d, want 1/1", outer, holes)
	}
	for _, l := range res.Loops {
		want := 12
		if l.Kind == Hole {
			want = 4
		}
		if l.Len() != want {
			t.Fatalf("%s loop len = %d, want %d", l.Kind, l.Len(), want)
		}
	}
	// The background model must see exactly one interior component of 1 cell.
	interior := 0
	for _, c := range res.Background {
		if !c.Exterior {
			interior++
			if c.Area != 1 {
				t.Fatalf("interior component area = %d, want 1", c.Area)
			}
		}
	}
	if interior != 1 {
		t.Fatalf("interior components = %d, want 1", interior)
	}
	mustValid(t, res)
}

// A one-cell-wide slit connects the inner background to the exterior: no
// hole, a single outer loop that travels in and back out of the slit.
func TestSlit(t *testing.T) {
	res := Analyze(FromRows(
		"#####",
		"#...#",
		"#...#",
		"#...#",
		"##.##",
	))
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	reg := res.Regions[0]
	if reg.Area != 15 || reg.Holes != 0 {
		t.Fatalf("area=%d holes=%d, want 15/0", reg.Area, reg.Holes)
	}
	outer, holes := loopKinds(res)
	if outer != 1 || holes != 0 {
		t.Fatalf("outer=%d holes=%d, want 1/0", outer, holes)
	}
	for _, c := range res.Background {
		if !c.Exterior {
			t.Fatalf("component %d is interior, want all exterior (slit)", c.ID)
		}
	}
	// The single loop must travel into the slit and back out: it reaches
	// the slit bottom vertices (2,5) and (3,5) on the image border.
	l := res.Loops[0]
	seen := make(map[Point]bool)
	for _, p := range l.Points {
		seen[p] = true
	}
	if !seen[Point{2, 5}] || !seen[Point{3, 5}] {
		t.Fatalf("loop does not reach the slit bottom: %v", l.Points)
	}
	if l.Len() != reg.Perimeter {
		t.Fatalf("loop len = %d, perimeter = %d", l.Len(), reg.Perimeter)
	}
	mustValid(t, res)
}

// The region's boundary touches itself at vertex (1,1): the outer loop and
// the hole loop share that vertex but stay separate rings.
func TestSelfTouchingCorner(t *testing.T) {
	res := Analyze(FromRows(
		".##",
		"#.#",
		"###",
	))
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	reg := res.Regions[0]
	if reg.Area != 7 || reg.Holes != 1 {
		t.Fatalf("area=%d holes=%d, want 7/1", reg.Area, reg.Holes)
	}
	outer, holes := loopKinds(res)
	if outer != 1 || holes != 1 {
		t.Fatalf("outer=%d holes=%d, want 1/1", outer, holes)
	}
	pinch := Point{1, 1}
	for _, l := range res.Loops {
		found := false
		for _, p := range l.Points {
			if p == pinch {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s loop does not pass through self-contact vertex %v", l.Kind, pinch)
		}
	}
	mustValid(t, res)
}

// A plus-shaped single-pixel-wide hole: one interior background component,
// one hole loop of length 12.
func TestPlusShapedHole(t *testing.T) {
	res := Analyze(FromRows(
		".###.",
		"##.##",
		"#...#",
		"##.##",
		".###.",
	))
	if len(res.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(res.Regions))
	}
	reg := res.Regions[0]
	if reg.Area != 16 || reg.Holes != 1 {
		t.Fatalf("area=%d holes=%d, want 16/1", reg.Area, reg.Holes)
	}
	outer, holes := loopKinds(res)
	if outer != 1 || holes != 1 {
		t.Fatalf("outer=%d holes=%d, want 1/1", outer, holes)
	}
	for _, l := range res.Loops {
		if l.Kind == Hole && l.Len() != 12 {
			t.Fatalf("hole loop len = %d, want 12", l.Len())
		}
	}
	mustValid(t, res)
}

// An interior background component adjacent to several regions at once
// (here: the enclosing frame plus four corner blocks) cannot be attributed
// to a single region; the cross-validation must flag the ambiguity for the
// blocks, which see an interior component but own no hole loop.
func TestSharedHoleIsFlagged(t *testing.T) {
	res := Analyze(FromRows(
		"#########",
		"#.......#",
		"#.##.##.#",
		"#.##.##.#",
		"#.......#",
		"#.##.##.#",
		"#.##.##.#",
		"#.......#",
		"#########",
	))
	if len(res.Regions) != 5 {
		t.Fatalf("regions = %d, want 5 (frame + 4 blocks)", len(res.Regions))
	}
	interior := 0
	for _, c := range res.Background {
		if !c.Exterior {
			interior++
			if len(c.Regions) != 5 {
				t.Fatalf("shared hole adjacent to %d regions, want 5", len(c.Regions))
			}
		}
	}
	if interior != 1 {
		t.Fatalf("interior components = %d, want 1", interior)
	}
	if len(res.Validate()) == 0 {
		t.Fatal("expected validation to flag the blocks' unenclosed interior component")
	}
}

// Loops are replayable: Moves -> Replay reproduces the vertices, and the
// replayed edges of all loops tile the boundary edge set exactly once.
func TestReplayRoundTrip(t *testing.T) {
	images := [][]string{
		{"#"},
		{"###", "#.#", "###"},
		{".##", "#.#", "###"},
		{"#####", "#...#", "#...#", "#...#", "##.##"},
		{"#..", ".#.", "..#"},
		{"####", "#..#", "#.##", "###."},
	}
	for _, rows := range images {
		res := Analyze(FromRows(rows...))
		for _, l := range res.Loops {
			pts, err := Replay(l.Points[0], l.Moves())
			if err != nil {
				t.Fatalf("%v: replay failed: %v", rows, err)
			}
			if !reflect.DeepEqual(pts, l.Points) {
				t.Fatalf("%v: replay = %v, want %v", rows, pts, l.Points)
			}
		}
		mustValid(t, res)
	}
}

// Region attribution: each loop reports the region whose cell lies on its
// left, and region loop sets partition the loop list.
func TestRegionAttribution(t *testing.T) {
	res := Analyze(FromRows(
		"###..#",
		"#.#..#",
		"###...",
	))
	if len(res.Regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(res.Regions))
	}
	seen := make(map[int]int)
	for _, reg := range res.Regions {
		for _, li := range reg.Loops {
			if seen[li] != 0 {
				t.Fatalf("loop %d attributed to multiple regions", li)
			}
			seen[li] = 1
			if res.Loops[li].Region != reg.ID {
				t.Fatalf("loop %d region = %d, listed under region %d", li, res.Loops[li].Region, reg.ID)
			}
		}
	}
	if len(seen) != len(res.Loops) {
		t.Fatalf("regions list %d loops, result has %d", len(seen), len(res.Loops))
	}
	var ring *Region
	for _, reg := range res.Regions {
		if reg.Holes == 1 {
			ring = reg
		}
	}
	if ring == nil || ring.Area != 8 {
		t.Fatalf("ring region = %+v, want one region with 1 hole and area 8", ring)
	}
	mustValid(t, res)
}
