package pathfill

import (
	"math"
	"testing"
)

func TestCoordinatesOutsideCanvas(t *testing.T) {
	const w, h = 20, 20
	p := &Path{}
	p.MoveTo(Point{-50, -30})
	p.LineTo(Point{60, -30})
	p.LineTo(Point{60, 45})
	p.LineTo(Point{-50, 45})
	p.Close()
	r := NewRasterizer(w, h)
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)
	for i, b := range mask {
		if b != 255 {
			t.Fatalf("clipped pixel %d = %d, want 255", i, b)
		}
	}

	cp := circlePath(-4, -4, 10)
	r2 := NewRasterizer(w, h)
	r2.Flatness = 0.1
	r2.AddPath(cp)
	m2 := r2.Alpha(FillNonZero)
	ref := referenceMask(w, h, 16, FillNonZero, cp)
	stats := compareMask(m2, ref)
	if stats.mae > 0.012 || stats.maxErr > 0.15 {
		t.Errorf("off-canvas circle stats = %+v", stats)
	}

	// A shape entirely outside the canvas yields an empty mask without panic.
	empty := &Path{}
	empty.MoveTo(Point{-30, -30})
	empty.LineTo(Point{-20, -30})
	empty.LineTo(Point{-20, -20})
	empty.Close()
	r3 := NewRasterizer(w, h)
	r3.AddPath(empty)
	m3 := r3.Alpha(FillNonZero)
	for i, b := range m3 {
		if b != 0 {
			t.Fatalf("outside pixel %d = %d, want 0", i, b)
		}
	}
}

func TestFlattenTolerance(t *testing.T) {
	loose := NewRasterizer(50, 50)
	loose.Flatness = 2
	loose.AddPath(circlePath(25, 25, 20))
	looseMask := loose.Alpha(FillNonZero)

	tight := NewRasterizer(50, 50)
	tight.Flatness = 0.05
	tight.AddPath(circlePath(25, 25, 20))
	tightMask := tight.Alpha(FillNonZero)

	ref := referenceMask(50, 50, 16, FillNonZero, circlePath(25, 25, 20))
	looseStats := compareMask(looseMask, ref)
	tightStats := compareMask(tightMask, ref)
	if tightStats.mae >= looseStats.mae {
		t.Errorf("tighter flatness did not improve MAE: tight %.5f loose %.5f",
			tightStats.mae, looseStats.mae)
	}
	if tightStats.mae > 0.01 {
		t.Errorf("tight flatness MAE = %.5f", tightStats.mae)
	}
}

func TestSharedEdgeNoSeam(t *testing.T) {
	const w, h = 30, 30

	// Two triangles sharing the diagonal from (8,5) to (22,25).
	left := &Path{}
	left.MoveTo(Point{8, 5})
	left.LineTo(Point{4, 27})
	left.LineTo(Point{22, 25})
	left.Close()

	right := &Path{}
	right.MoveTo(Point{8, 5})
	right.LineTo(Point{22, 25})
	right.LineTo(Point{27, 3})
	right.Close()

	r1 := NewRasterizer(w, h)
	r1.AddPath(left)
	m1 := r1.Alpha(FillNonZero)
	r2 := NewRasterizer(w, h)
	r2.AddPath(right)
	m2 := r2.Alpha(FillNonZero)

	// Rasterizing both contours together gives the union.
	ru := NewRasterizer(w, h)
	ru.AddPath(left)
	ru.AddPath(right)
	union := ru.Alpha(FillNonZero)

	gaps := 0
	var maxGap float64
	for i := range union {
		sum := float64(m1[i])/255 + float64(m2[i])/255
		if sum > 1 {
			sum = 1
		}
		if union[i] == 255 && sum < 0.98 {
			gaps++
			if d := 0.98 - sum; d > maxGap {
				maxGap = d
			}
		}
	}
	if gaps > 0 {
		t.Errorf("shared edge produced %d seam pixels (max gap %.3f)", gaps, maxGap)
	}

	// Shared vertical edge on fractional coordinates: adjacent rectangles.
	rectA := &Path{}
	rectA.MoveTo(Point{3.5, 4.5})
	rectA.LineTo(Point{15.5, 4.5})
	rectA.LineTo(Point{15.5, 22.5})
	rectA.LineTo(Point{3.5, 22.5})
	rectA.Close()
	rectB := &Path{}
	rectB.MoveTo(Point{15.5, 4.5})
	rectB.LineTo(Point{26.5, 4.5})
	rectB.LineTo(Point{26.5, 22.5})
	rectB.LineTo(Point{15.5, 22.5})
	rectB.Close()

	ra := NewRasterizer(w, h)
	ra.AddPath(rectA)
	ma := ra.Alpha(FillNonZero)
	rb := NewRasterizer(w, h)
	rb.AddPath(rectB)
	mb := rb.Alpha(FillNonZero)

	ru2 := NewRasterizer(w, h)
	ru2.AddPath(rectA)
	ru2.AddPath(rectB)
	mu := ru2.Alpha(FillNonZero)

	for y := 5; y < 22; y++ {
		for x := 4; x < 26; x++ {
			sum := float64(ma[y*w+x])/255 + float64(mb[y*w+x])/255
			if sum > 1 {
				sum = 1
			}
			// The two fills are disjoint apart from the shared boundary, so
			// their coverages add up to the merged fill with no gap.
			if math.Abs(sum-float64(mu[y*w+x])/255) > 2.0/255 {
				t.Fatalf("vertical seam at (%d,%d): combined %.4f vs merged %.4f",
					x, y, sum, float64(mu[y*w+x])/255)
			}
			if x >= 4 && x <= 25 && sum < 0.99 {
				t.Fatalf("seam gap at (%d,%d): coverage %.4f", x, y, sum)
			}
		}
	}
}

func TestZeroSizeAndDegenerate(t *testing.T) {
	r := NewRasterizer(0, 0)
	if got := r.Alpha(FillNonZero); len(got) != 0 {
		t.Fatalf("zero-size mask length = %d", len(got))
	}

	// Degenerate path: zero-length edges and a point.
	r2 := NewRasterizer(8, 8)
	p := &Path{}
	p.MoveTo(Point{3, 3})
	p.LineTo(Point{3, 3})
	p.QuadTo(Point{4, 4}, Point{4, 4})
	p.Close()
	r2.AddPath(p)
	mask := r2.Alpha(FillEvenOdd)
	for i, b := range mask {
		if b != 0 {
			t.Fatalf("degenerate pixel %d = %d, want 0", i, b)
		}
	}
}

func TestResetReuse(t *testing.T) {
	r := NewRasterizer(10, 10)
	p := &Path{}
	p.MoveTo(Point{1, 1})
	p.LineTo(Point{9, 1})
	p.LineTo(Point{9, 9})
	p.LineTo(Point{1, 9})
	p.Close()
	r.AddPath(p)
	first := r.Alpha(FillNonZero)

	r.Reset()
	second := r.Alpha(FillNonZero)
	for i := range second {
		if second[i] != 0 {
			t.Fatalf("reset left pixel %d = %d", i, second[i])
		}
	}

	r.AddPath(p)
	third := r.Alpha(FillNonZero)
	for i := range third {
		if third[i] != first[i] {
			t.Fatalf("reused rasterizer differs at %d", i)
		}
	}
}
