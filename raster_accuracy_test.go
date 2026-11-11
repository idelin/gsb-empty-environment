package pathfill

import (
	"math"
	"testing"
)

func TestCircleMatchesSupersampled(t *testing.T) {
	const w, h = 40, 40
	p := circlePath(20, 20, 12.7)
	r := NewRasterizer(w, h)
	r.Flatness = 0.1
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)

	ref := referenceMask(w, h, 16, FillNonZero, p)
	stats := compareMask(mask, ref)
	if stats.mae > 0.01 {
		t.Errorf("circle MAE = %.4f, want <= 0.01", stats.mae)
	}
	if stats.maxErr > 0.12 {
		t.Errorf("circle max error = %.4f, want <= 0.12", stats.maxErr)
	}
	if stats.badPixels != 0 {
		t.Errorf("circle has %d pixels with error > 0.15", stats.badPixels)
	}

	var area float64
	for _, b := range mask {
		area += float64(b) / 255
	}
	want := math.Pi * 12.7 * 12.7
	if math.Abs(area-want)/want > 0.01 {
		t.Errorf("circle area = %.2f, want ~%.2f", area, want)
	}
}

func TestQuadraticMatchesSupersampled(t *testing.T) {
	const w, h = 36, 36
	p := quadHeartPath(18, 18, 10)
	r := NewRasterizer(w, h)
	r.Flatness = 0.1
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)

	ref := referenceMask(w, h, 16, FillNonZero, p)
	stats := compareMask(mask, ref)
	if stats.mae > 0.012 {
		t.Errorf("quadratic MAE = %.4f", stats.mae)
	}
	if stats.maxErr > 0.12 {
		t.Errorf("quadratic max error = %.4f", stats.maxErr)
	}
}

func TestFillRulesOnStar(t *testing.T) {
	const w, h = 44, 44
	star := starPath(22, 22, 18, 8, 5, math.Pi/2)

	for _, rule := range []FillRule{FillNonZero, FillEvenOdd} {
		r := NewRasterizer(w, h)
		r.AddPath(star)
		mask := r.Alpha(rule)
		ref := referenceMask(w, h, 16, rule, star)
		stats := compareMask(mask, ref)
		if stats.mae > 0.02 {
			t.Errorf("rule %d MAE = %.4f", rule, stats.mae)
		}
		if stats.maxErr > 0.2 {
			t.Errorf("rule %d max error = %.4f", rule, stats.maxErr)
		}
	}

	// Two same-winding squares: the overlap has winding 2, filled under
	// non-zero but empty under even-odd.
	squares := &Path{}
	squares.MoveTo(Point{6, 6})
	squares.LineTo(Point{26, 6})
	squares.LineTo(Point{26, 26})
	squares.LineTo(Point{6, 26})
	squares.Close()
	squares.MoveTo(Point{18, 18})
	squares.LineTo(Point{38, 18})
	squares.LineTo(Point{38, 38})
	squares.LineTo(Point{18, 38})
	squares.Close()

	nz := NewRasterizer(w, h)
	nz.AddPath(squares)
	nzMask := nz.Alpha(FillNonZero)
	eo := NewRasterizer(w, h)
	eo.AddPath(squares)
	eoMask := eo.Alpha(FillEvenOdd)
	if nzMask[22*w+22] < 250 {
		t.Errorf("non-zero overlap = %d, want opaque", nzMask[22*w+22])
	}
	if eoMask[22*w+22] > 5 {
		t.Errorf("even-odd overlap = %d, want transparent", eoMask[22*w+22])
	}
}

func TestOverlappingSubpaths(t *testing.T) {
	const w, h = 40, 40
	p := &Path{}
	for _, c := range []Point{{16, 20}, {24, 20}} {
		circle := circlePath(c.X, c.Y, 11)
		p.verbs = append(p.verbs, circle.verbs...)
		p.points = append(p.points, circle.points...)
	}

	for _, rule := range []FillRule{FillNonZero, FillEvenOdd} {
		r := NewRasterizer(w, h)
		r.Flatness = 0.1
		r.AddPath(p)
		mask := r.Alpha(rule)
		ref := referenceMask(w, h, 12, rule, p)
		stats := compareMask(mask, ref)
		if stats.mae > 0.015 {
			t.Errorf("overlap rule %d MAE = %.4f", rule, stats.mae)
		}
		if stats.maxErr > 0.15 {
			t.Errorf("overlap rule %d max error = %.4f", rule, stats.maxErr)
		}
	}
}

func TestOpposingWindingHole(t *testing.T) {
	const w, h = 30, 30
	p := &Path{}
	p.MoveTo(Point{2, 2})
	p.LineTo(Point{28, 2})
	p.LineTo(Point{28, 28})
	p.LineTo(Point{2, 28})
	p.Close()
	// Inner square with reversed winding carves a hole under non-zero.
	p.MoveTo(Point{10, 10})
	p.LineTo(Point{10, 20})
	p.LineTo(Point{20, 20})
	p.LineTo(Point{20, 10})
	p.Close()

	r := NewRasterizer(w, h)
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)
	if got := mask[15*w+15]; got != 0 {
		t.Errorf("hole center = %d, want 0", got)
	}
	if got := mask[5*w+5]; got != 255 {
		t.Errorf("outer region = %d, want 255", got)
	}

	ref := referenceMask(w, h, 12, FillNonZero, p)
	stats := compareMask(mask, ref)
	if stats.mae > 0.005 {
		t.Errorf("hole MAE = %.4f", stats.mae)
	}
}
