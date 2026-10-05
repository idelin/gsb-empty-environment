package dt

import (
	"math/rand"
	"testing"
)

// brute is the reference O(pixels * foreground) implementation.
func brute(fg [][]bool) Result {
	h := len(fg)
	w := 0
	if h > 0 {
		w = len(fg[0])
	}
	dist := make([][]int64, h)
	srcR := make([][]int, h)
	srcC := make([][]int, h)
	var pts [][2]int
	for r := 0; r < h; r++ {
		dist[r] = make([]int64, w)
		srcR[r] = make([]int, w)
		srcC[r] = make([]int, w)
		for c := 0; c < w; c++ {
			if fg[r][c] {
				pts = append(pts, [2]int{r, c})
			}
		}
	}
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			if len(pts) == 0 {
				dist[r][c] = NoDist
				srcR[r][c] = NoSource
				srcC[r][c] = NoSource
				continue
			}
			best := int64(-1)
			br, bc := 0, 0
			for _, p := range pts {
				dr := int64(r - p[0])
				dc := int64(c - p[1])
				d := dr*dr + dc*dc
				if best < 0 || d < best || (d == best && (p[0] < br || (p[0] == br && p[1] < bc))) {
					best, br, bc = d, p[0], p[1]
				}
			}
			dist[r][c] = best
			srcR[r][c] = br
			srcC[r][c] = bc
		}
	}
	return Result{Dist: dist, SrcR: srcR, SrcC: srcC}
}

func checkEqual(t *testing.T, fg [][]bool) {
	t.Helper()
	got := Transform(fg)
	want := brute(fg)
	for r := range want.Dist {
		for c := range want.Dist[r] {
			if got.Dist[r][c] != want.Dist[r][c] ||
				got.SrcR[r][c] != want.SrcR[r][c] ||
				got.SrcC[r][c] != want.SrcC[r][c] {
				t.Fatalf("pixel (%d,%d): got (d=%d src=%d,%d), want (d=%d src=%d,%d); fg=%v",
					r, c,
					got.Dist[r][c], got.SrcR[r][c], got.SrcC[r][c],
					want.Dist[r][c], want.SrcR[r][c], want.SrcC[r][c],
					fg)
			}
		}
	}
}

// TestExhaustive compares against brute force on every binary image of
// several small sizes, which includes the no-foreground, single-row,
// single-column and equidistant-boundary cases.
func TestExhaustive(t *testing.T) {
	sizes := [][2]int{
		{1, 1}, {1, 2}, {2, 1}, {1, 4}, {4, 1},
		{2, 2}, {2, 3}, {3, 2}, {3, 3}, {2, 4}, {4, 2},
	}
	for _, sz := range sizes {
		h, w := sz[0], sz[1]
		n := h * w
		for mask := 0; mask < 1<<n; mask++ {
			fg := make([][]bool, h)
			for r := 0; r < h; r++ {
				fg[r] = make([]bool, w)
				for c := 0; c < w; c++ {
					fg[r][c] = mask&(1<<(r*w+c)) != 0
				}
			}
			checkEqual(t, fg)
		}
	}
}

// TestRandom compares against brute force on random images of larger
// sizes and varied foreground densities.
func TestRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	sizes := [][2]int{{1, 30}, {30, 1}, {5, 7}, {8, 8}, {13, 11}, {20, 25}, {40, 35}}
	for _, sz := range sizes {
		h, w := sz[0], sz[1]
		for iter := 0; iter < 40; iter++ {
			// Density varies from very sparse to very dense.
			den := rng.Float64()
			fg := make([][]bool, h)
			for r := 0; r < h; r++ {
				fg[r] = make([]bool, w)
				for c := 0; c < w; c++ {
					fg[r][c] = rng.Float64() < den
				}
			}
			checkEqual(t, fg)
		}
	}
}

func TestNoForeground(t *testing.T) {
	fg := [][]bool{{false, false}, {false, false}}
	got := Transform(fg)
	for r := 0; r < 2; r++ {
		for c := 0; c < 2; c++ {
			if got.Dist[r][c] != NoDist || got.SrcR[r][c] != NoSource || got.SrcC[r][c] != NoSource {
				t.Fatalf("pixel (%d,%d): got (d=%d src=%d,%d), want no-source sentinels",
					r, c, got.Dist[r][c], got.SrcR[r][c], got.SrcC[r][c])
			}
		}
	}
}

func TestEmptyImage(t *testing.T) {
	got := Transform(nil)
	if len(got.Dist) != 0 || len(got.SrcR) != 0 || len(got.SrcC) != 0 {
		t.Fatalf("empty image: got non-empty result")
	}
}

func TestSingleRow(t *testing.T) {
	// Foreground at columns 1 and 4.
	fg := [][]bool{{false, true, false, false, true, false}}
	got := Transform(fg)
	wantDist := []int64{1, 0, 1, 1, 0, 1}
	wantC := []int{1, 1, 1, 4, 4, 4} // boundary between sources 1 and 4 is at 2.5
	for c := range wantDist {
		if got.Dist[0][c] != wantDist[c] || got.SrcR[0][c] != 0 || got.SrcC[0][c] != wantC[c] {
			t.Fatalf("col %d: got (d=%d src=%d,%d), want (d=%d src=0,%d)",
				c, got.Dist[0][c], got.SrcR[0][c], got.SrcC[0][c], wantDist[c], wantC[c])
		}
	}
	// Foreground at columns 0 and 2: column 1 is exactly equidistant and
	// must pick the smaller source column.
	fg2 := [][]bool{{true, false, true}}
	got2 := Transform(fg2)
	if got2.Dist[0][1] != 1 || got2.SrcC[0][1] != 0 {
		t.Fatalf("tie midpoint: got (d=%d src=0,%d), want (d=1 src=0,0)",
			got2.Dist[0][1], got2.SrcC[0][1])
	}
}

func TestSingleColumn(t *testing.T) {
	fg := [][]bool{{false}, {true}, {false}, {false}, {true}, {false}}
	got := Transform(fg)
	wantDist := []int64{1, 0, 1, 1, 0, 1}
	wantR := []int{1, 1, 1, 4, 4, 4}
	for r := range wantDist {
		if got.Dist[r][0] != wantDist[r] || got.SrcR[r][0] != wantR[r] || got.SrcC[r][0] != 0 {
			t.Fatalf("row %d: got (d=%d src=%d,%d), want (d=%d src=%d,0)",
				r, got.Dist[r][0], got.SrcR[r][0], got.SrcC[r][0], wantDist[r], wantR[r])
		}
	}
}

func TestAllForeground(t *testing.T) {
	fg := [][]bool{{true, true, true}, {true, true, true}}
	got := Transform(fg)
	for r := 0; r < 2; r++ {
		for c := 0; c < 3; c++ {
			if got.Dist[r][c] != 0 || got.SrcR[r][c] != r || got.SrcC[r][c] != c {
				t.Fatalf("pixel (%d,%d): got (d=%d src=%d,%d), want (d=0 src=%d,%d)",
					r, c, got.Dist[r][c], got.SrcR[r][c], got.SrcC[r][c], r, c)
			}
		}
	}
}

// TestEquidistant2D checks that among equally near foreground pixels the
// lexicographically smallest (row, column) wins, and in particular that
// the order is row-major rather than column-major.
func TestEquidistant2D(t *testing.T) {
	// Foreground at (1,0) and (0,1); both are at distance 1 from (0,0).
	// Lexicographically smallest source is (0,1), not (1,0).
	fg := [][]bool{
		{false, true},
		{true, false},
	}
	got := Transform(fg)
	if got.Dist[0][0] != 1 || got.SrcR[0][0] != 0 || got.SrcC[0][0] != 1 {
		t.Fatalf("got (d=%d src=%d,%d), want (d=1 src=0,1)",
			got.Dist[0][0], got.SrcR[0][0], got.SrcC[0][0])
	}

	// Foreground at (0,2) and (2,0); center (1,1) is equidistant (d=2)
	// from both and must pick (0,2). Corners (0,0) and (2,2) are
	// equidistant (d=4) from both and must also pick (0,2).
	fg2 := [][]bool{
		{false, false, true},
		{false, false, false},
		{true, false, false},
	}
	got2 := Transform(fg2)
	for _, p := range [][2]int{{1, 1}, {0, 0}, {2, 2}} {
		r, c := p[0], p[1]
		if got2.SrcR[r][c] != 0 || got2.SrcC[r][c] != 2 {
			t.Fatalf("pixel (%d,%d): got src=(%d,%d), want (0,2)",
				r, c, got2.SrcR[r][c], got2.SrcC[r][c])
		}
	}
	if got2.Dist[1][1] != 2 || got2.Dist[0][0] != 4 || got2.Dist[2][2] != 4 {
		t.Fatalf("unexpected distances: %v", got2.Dist)
	}
}

// TestKnownPattern checks a full small map computed by hand.
func TestKnownPattern(t *testing.T) {
	fg := [][]bool{
		{false, false, false},
		{false, true, false},
		{false, false, false},
	}
	got := Transform(fg)
	want := [][]int64{
		{2, 1, 2},
		{1, 0, 1},
		{2, 1, 2},
	}
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			if got.Dist[r][c] != want[r][c] || got.SrcR[r][c] != 1 || got.SrcC[r][c] != 1 {
				t.Fatalf("pixel (%d,%d): got (d=%d src=%d,%d), want (d=%d src=1,1)",
					r, c, got.Dist[r][c], got.SrcR[r][c], got.SrcC[r][c], want[r][c])
			}
		}
	}
}
