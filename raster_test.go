package pathfill

import (
	"math"
	"testing"
)

func circlePath(cx, cy, radius float64) *Path {
	// Approximate a circle with four cubic Bézier segments.
	k := 0.5522847498307936 * radius
	p := &Path{}
	p.MoveTo(Point{cx + radius, cy})
	p.CubicTo(Point{cx + radius, cy + k}, Point{cx + k, cy + radius}, Point{cx, cy + radius})
	p.CubicTo(Point{cx - k, cy + radius}, Point{cx - radius, cy + k}, Point{cx - radius, cy})
	p.CubicTo(Point{cx - radius, cy - k}, Point{cx - k, cy - radius}, Point{cx, cy - radius})
	p.CubicTo(Point{cx + k, cy - radius}, Point{cx + radius, cy - k}, Point{cx + radius, cy})
	p.Close()
	return p
}

func quadHeartPath(cx, cy, s float64) *Path {
	p := &Path{}
	p.MoveTo(Point{cx, cy + s})
	p.QuadTo(Point{cx - 1.5*s, cy + 0.2*s}, Point{cx - s, cy - s})
	p.QuadTo(Point{cx - s, cy - 1.6*s}, Point{cx, cy - 0.8*s})
	p.QuadTo(Point{cx + s, cy - 1.6*s}, Point{cx + s, cy - s})
	p.QuadTo(Point{cx + 1.5*s, cy + 0.2*s}, Point{cx, cy + s})
	p.Close()
	return p
}

func starPath(cx, cy, rOuter, rInner float64, points int, rot float64) *Path {
	p := &Path{}
	for i := 0; i < points*2; i++ {
		radius := rOuter
		if i&1 == 1 {
			radius = rInner
		}
		a := rot + float64(i)*math.Pi/float64(points)
		pt := Point{cx + radius*math.Cos(a), cy + radius*math.Sin(a)}
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	p.Close()
	return p
}

func TestSolidRectangle(t *testing.T) {
	const w, h = 12, 10
	r := NewRasterizer(w, h)
	p := &Path{}
	p.MoveTo(Point{2, 3})
	p.LineTo(Point{8, 3})
	p.LineTo(Point{8, 7})
	p.LineTo(Point{2, 7})
	p.Close()
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)

	if len(mask) != w*h {
		t.Fatalf("mask length = %d, want %d", len(mask), w*h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			inside := x >= 2 && x < 8 && y >= 3 && y < 7
			want := byte(0)
			if inside {
				want = 255
			}
			if got := mask[y*w+x]; got != want {
				t.Errorf("pixel (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestFractionalRectangleCoverage(t *testing.T) {
	const w, h = 4, 4
	r := NewRasterizer(w, h)
	p := &Path{}
	p.MoveTo(Point{0.5, 0.5})
	p.LineTo(Point{2.5, 0.5})
	p.LineTo(Point{2.5, 2.5})
	p.LineTo(Point{0.5, 2.5})
	p.Close()
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)

	if got := float64(mask[0]) / 255; math.Abs(got-0.25) > 1.5/255 {
		t.Errorf("corner coverage = %.4f, want ~0.25", got)
	}
	if got := mask[1*w+1]; got != 255 {
		t.Errorf("interior coverage = %d, want 255", got)
	}
	if got := float64(mask[0*w+1]) / 255; math.Abs(got-0.5) > 1.5/255 {
		t.Errorf("edge coverage = %.4f, want ~0.5", got)
	}
}
