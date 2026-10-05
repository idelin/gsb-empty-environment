package edt

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce is the O(pixels^2) reference implementation.
func bruteForce(fg [][]bool) ([][]int64, [][]Point) {
	h := len(fg)
	w := 0
	if h > 0 {
		w = len(fg[0])
	}
	var pts []Point
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			if fg[r][c] {
				pts = append(pts, Point{r, c})
			}
		}
	}
	dist := make([][]int64, h)
	src := make([][]Point, h)
	for r := 0; r < h; r++ {
		dist[r] = make([]int64, w)
		src[r] = make([]Point, w)
		for c := 0; c < w; c++ {
			if len(pts) == 0 {
				dist[r][c] = -1
				src[r][c] = Point{-1, -1}
				continue
			}
			best := int64(-1)
			var bp Point
			for _, p := range pts {
				dr := int64(r - p.Row)
				dc := int64(c - p.Col)
				d := dr*dr + dc*dc
				if best < 0 || d < best ||
					(d == best && (p.Row < bp.Row ||
						(p.Row == bp.Row && p.Col < bp.Col))) {
					best = d
					bp = p
				}
			}
			dist[r][c] = best
			src[r][c] = bp
		}
	}
	return dist, src
}

func checkAgainstBrute(t *testing.T, fg [][]bool) {
	t.Helper()
	wantD, wantS := bruteForce(fg)
	gotD, gotS := DistanceTransform(fg)
	for r := range wantD {
		for c := range wantD[r] {
			if gotD[r][c] != wantD[r][c] || gotS[r][c] != wantS[r][c] {
				t.Fatalf("pixel (%d,%d): got (dist=%d, src=%v), want (dist=%d, src=%v)",
					r, c, gotD[r][c], gotS[r][c], wantD[r][c], wantS[r][c])
			}
		}
	}
}

func makeImage(h, w int, pts ...Point) [][]bool {
	fg := make([][]bool, h)
	for r := range fg {
		fg[r] = make([]bool, w)
	}
	for _, p := range pts {
		fg[p.Row][p.Col] = true
	}
	return fg
}

func TestNoForeground(t *testing.T) {
	dist, src := DistanceTransform(makeImage(3, 5))
	for r := 0; r < 3; r++ {
		for c := 0; c < 5; c++ {
			if dist[r][c] != -1 || src[r][c] != (Point{-1, -1}) {
				t.Fatalf("pixel (%d,%d): got (dist=%d, src=%v), want (-1, {-1 -1})",
					r, c, dist[r][c], src[r][c])
			}
		}
	}
}

func TestEmpty(t *testing.T) {
	if d, s := DistanceTransform(nil); len(d) != 0 || len(s) != 0 {
		t.Fatal("nil image should give empty result")
	}
	if d, s := DistanceTransform(makeImage(2, 0)); len(d) != 2 || len(d[0]) != 0 || len(s) != 2 {
		t.Fatal("zero-width image should give empty rows")
	}
}

func TestSinglePixel(t *testing.T) {
	checkAgainstBrute(t, makeImage(1, 1, Point{0, 0}))
	checkAgainstBrute(t, makeImage(1, 1))
}

func TestSingleRow(t *testing.T) {
	fg := makeImage(1, 7, Point{0, 0}, Point{0, 5})
	dist, src := DistanceTransform(fg)
	wantD := []int64{0, 1, 4, 4, 1, 0, 1}
	wantS := []Point{{0, 0}, {0, 0}, {0, 0}, {0, 5}, {0, 5}, {0, 5}, {0, 5}}
	for c := 0; c < 7; c++ {
		if dist[0][c] != wantD[c] || src[0][c] != wantS[c] {
			t.Fatalf("col %d: got (dist=%d, src=%v), want (dist=%d, src=%v)",
				c, dist[0][c], src[0][c], wantD[c], wantS[c])
		}
	}
}

func TestSingleColumn(t *testing.T) {
	checkAgainstBrute(t, makeImage(9, 1, Point{2, 0}, Point{7, 0}))
	checkAgainstBrute(t, makeImage(9, 1, Point{8, 0}))
}

// TestTieBreakEquidistant covers pixels sitting exactly on the bisector of
// two foreground pixels: the lexicographically smaller source must win.
func TestTieBreakEquidistant(t *testing.T) {
	// Horizontal bisector: (0,1) is equidistant from (0,0) and (0,2).
	dist, src := DistanceTransform(makeImage(1, 3, Point{0, 0}, Point{0, 2}))
	if dist[0][1] != 1 || src[0][1] != (Point{0, 0}) {
		t.Fatalf("got (dist=%d, src=%v), want (1, {0 0})", dist[0][1], src[0][1])
	}

	// Vertical bisector: (1,0) is equidistant from (0,0) and (2,0).
	dist, src = DistanceTransform(makeImage(3, 1, Point{0, 0}, Point{2, 0}))
	if dist[1][0] != 1 || src[1][0] != (Point{0, 0}) {
		t.Fatalf("got (dist=%d, src=%v), want (1, {0 0})", dist[1][0], src[1][0])
	}

	// Diagonal: every background pixel of this 2x2 is equidistant from
	// (0,1) and (1,0); (0,1) is lexicographically smaller.
	fg := makeImage(2, 2, Point{0, 1}, Point{1, 0})
	dist, src = DistanceTransform(fg)
	for _, p := range []Point{{0, 0}, {1, 1}} {
		if dist[p.Row][p.Col] != 1 || src[p.Row][p.Col] != (Point{0, 1}) {
			t.Fatalf("pixel %v: got (dist=%d, src=%v), want (1, {0 1})",
				p, dist[p.Row][p.Col], src[p.Row][p.Col])
		}
	}

	// Mixed distances with a tie across rows and columns: sources (0,2)
	// and (2,0) are both at squared distance 2 from (1,1).
	fg = makeImage(3, 3, Point{0, 2}, Point{2, 0})
	dist, src = DistanceTransform(fg)
	if dist[1][1] != 2 || src[1][1] != (Point{0, 2}) {
		t.Fatalf("got (dist=%d, src=%v), want (2, {0 2})", dist[1][1], src[1][1])
	}
}

// TestExhaustive compares against brute force on every binary image of a
// few small shapes, covering all tie and boundary configurations.
func TestExhaustive(t *testing.T) {
	for _, hw := range [][2]int{{1, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6},
		{2, 1}, {3, 1}, {4, 1}, {5, 1}, {6, 1},
		{2, 2}, {2, 3}, {3, 2}, {2, 4}, {4, 2}, {3, 3}, {4, 4}} {
		h, w := hw[0], hw[1]
		n := h * w
		t.Run(fmt.Sprintf("%dx%d", h, w), func(t *testing.T) {
			fg := makeImage(h, w)
			for mask := 0; mask < 1<<n; mask++ {
				for i := 0; i < n; i++ {
					fg[i/w][i%w] = mask>>i&1 == 1
				}
				checkAgainstBrute(t, fg)
			}
		})
	}
}

// TestRandom compares against brute force on random images, including
// larger ones with many equidistant configurations.
func TestRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		h := 1 + rng.Intn(30)
		w := 1 + rng.Intn(30)
		density := rng.Float64()
		fg := makeImage(h, w)
		for r := 0; r < h; r++ {
			for c := 0; c < w; c++ {
				fg[r][c] = rng.Float64() < density
			}
		}
		checkAgainstBrute(t, fg)
	}
}

func BenchmarkDistanceTransform(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{128, 256, 512, 1024, 2048} {
		fg := makeImage(n, n)
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				fg[r][c] = rng.Float64() < 0.05
			}
		}
		b.Run(fmt.Sprintf("%dx%d", n, n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				DistanceTransform(fg)
			}
		})
	}
}
