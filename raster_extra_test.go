package pathfill

import "testing"

func TestAddContourAndImplicitClose(t *testing.T) {
	const w, h = 10, 10

	r1 := NewRasterizer(w, h)
	r1.AddContour([]Point{
		{2, 2}, {8, 2}, {8, 8}, {2, 8},
	})
	m1 := r1.Alpha(FillNonZero)

	// Equivalent path without an explicit Close.
	p := &Path{}
	p.MoveTo(Point{2, 2})
	p.LineTo(Point{8, 2})
	p.LineTo(Point{8, 8})
	p.LineTo(Point{2, 8})
	r2 := NewRasterizer(w, h)
	r2.AddPath(p)
	m2 := r2.Alpha(FillNonZero)

	for i := range m1 {
		if m1[i] != m2[i] {
			t.Fatalf("AddContour vs implicit close differ at %d: %d vs %d", i, m1[i], m2[i])
		}
	}
	if m1[5*w+5] != 255 {
		t.Fatalf("interior = %d, want 255", m1[5*w+5])
	}

	// Explicitly-closed polyline with repeated start point.
	r3 := NewRasterizer(w, h)
	r3.AddContour([]Point{
		{2, 2}, {8, 2}, {8, 8}, {2, 8}, {2, 2},
	})
	m3 := r3.Alpha(FillNonZero)
	for i := range m3 {
		if m3[i] != m1[i] {
			t.Fatalf("closed contour differs at %d: %d vs %d", i, m3[i], m1[i])
		}
	}
}

func TestEvenOddCircle(t *testing.T) {
	const w, h = 30, 30
	p := circlePath(15, 15, 10.3)
	r := NewRasterizer(w, h)
	r.Flatness = 0.1
	r.AddPath(p)
	mask := r.Alpha(FillEvenOdd)
	ref := referenceMask(w, h, 16, FillEvenOdd, p)
	stats := compareMask(mask, ref)
	if stats.mae > 0.01 || stats.maxErr > 0.12 {
		t.Fatalf("even-odd circle stats = %+v", stats)
	}
}

func TestHugeCoordinates(t *testing.T) {
	const w, h = 16, 16
	// Path with coordinates orders of magnitude beyond the canvas; only
	// clipped portions are visible and no NaN/Inf must appear.
	p := &Path{}
	p.MoveTo(Point{-1e6, 3.25})
	p.LineTo(Point{1e6, 3.25})
	p.LineTo(Point{1e6, 11.75})
	p.LineTo(Point{-1e6, 11.75})
	p.Close()
	r := NewRasterizer(w, h)
	r.AddPath(p)
	mask := r.Alpha(FillNonZero)

	ref := &Path{}
	ref.MoveTo(Point{-20, 3.25})
	ref.LineTo(Point{36, 3.25})
	ref.LineTo(Point{36, 11.75})
	ref.LineTo(Point{-20, 11.75})
	ref.Close()
	rr := NewRasterizer(w, h)
	rr.AddPath(ref)
	want := rr.Alpha(FillNonZero)

	for i, b := range mask {
		if b != want[i] {
			t.Fatalf("huge-coordinate pixel %d = %d, want %d", i, b, want[i])
		}
	}

	for y := 4; y < 11; y++ {
		for x := 0; x < w; x++ {
			if mask[y*w+x] != 255 {
				t.Fatalf("interior pixel (%d,%d) = %d", x, y, mask[y*w+x])
			}
		}
	}
}

func TestPathReset(t *testing.T) {
	p := &Path{}
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{5, 0})
	p.Reset()
	if len(p.verbs) != 0 || len(p.points) != 0 || p.active {
		t.Fatal("Reset did not clear path state")
	}
	r := NewRasterizer(6, 6)
	r.AddPath(p)
	for _, b := range r.Alpha(FillNonZero) {
		if b != 0 {
			t.Fatal("empty path produced coverage")
		}
	}
}

func BenchmarkCircleFill(b *testing.B) {
	p := circlePath(128, 128, 90)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewRasterizer(256, 256)
		r.Flatness = 0.25
		r.AddPath(p)
		_ = r.Alpha(FillNonZero)
	}
}
