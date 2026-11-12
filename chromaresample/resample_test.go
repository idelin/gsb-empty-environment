package chromaresample

import (
	"math"
	"testing"
)

// format describes one chroma subsampling geometry against a reference
// (luma) grid of refW x refH pixels.
type format struct {
	name    string
	w, h    int
	centerX Rational
	centerY Rational
}

func mpegFormats(refW, refH int) map[string]format {
	return map[string]format{
		"444": {"444", refW, refH, Rational{0, 1}, Rational{0, 1}},
		"422": {"422", (refW + 1) / 2, refH, Rational{1, 2}, Rational{0, 1}},
		"420": {"420", (refW + 1) / 2, (refH + 1) / 2, Rational{1, 2}, Rational{1, 2}},
	}
}

func jpegFormats(refW, refH int) map[string]format {
	return map[string]format{
		"444": {"444", refW, refH, Rational{0, 1}, Rational{0, 1}},
		"422": {"422", (refW + 1) / 2, refH, Rational{0, 1}, Rational{0, 1}},
		"420": {"420", (refW + 1) / 2, (refH + 1) / 2, Rational{0, 1}, Rational{0, 1}},
	}
}

func geometryFor(refW, refH int, src, dst format) Geometry {
	return Geometry{
		RefWidth:  refW,
		RefHeight: refH,
		Src:       Grid{src.w, src.h, src.centerX, src.centerY},
		Dst:       Grid{dst.w, dst.h, dst.centerX, dst.centerY},
	}
}

func makePlane(g Grid, stridePad int) Plane {
	stride := g.Width + stridePad
	return Plane{
		Grid:   g,
		Pix:    make([]uint8, stride*g.Height),
		Stride: stride,
	}
}

func fillPlane(p Plane, values func(x, y int) uint8) {
	for y := 0; y < p.Height; y++ {
		for x := 0; x < p.Width; x++ {
			p.Pix[y*p.Stride+x] = values(x, y)
		}
	}
}

func TestMirrorIndex(t *testing.T) {
	want := []int{4, 3, 2, 1, 0, 0, 1, 2, 3, 4, 4, 3, 2, 1, 0}
	for i, w := range want {
		if got := mirrorIndex(i-5, 5); got != w {
			t.Fatalf("mirrorIndex(%d, 5) = %d, want %d", i-5, got, w)
		}
	}
	if mirrorIndex(7, 1) != 0 || mirrorIndex(-3, 1) != 0 {
		t.Fatal("mirrorIndex must pin every coordinate for n == 1")
	}
}

func TestRoundEvenAndSaturate(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0}, {0.4, 0}, {0.6, 1}, {0.5, 0}, {1.5, 2}, {2.5, 2},
		{-0.5, 0}, {-1.5, -2}, {-2.5, -2}, {254.5, 254}, {255.5, 256},
	}
	for _, c := range cases {
		if got := roundEven(c.in); got != c.want {
			t.Fatalf("roundEven(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   float64
		want uint8
	}{
		{-3, 0}, {0, 0}, {255, 255}, {300, 255}, {127.5, 128}, {128.5, 128},
	} {
		if got := saturate8(roundEven(c.in)); got != c.want {
			t.Fatalf("saturate8(roundEven(%v)) = %d, want %d", c.in, got, c.want)
		}
	}
}

// overlap1 returns the area-overlap fraction of source cell i with the
// output cell centered at uj, for box integration with spacing ratio scale.
// The returned length is in source-cell units; callers divide by scale.
func overlap1(i int, uj, scale float64) float64 {
	lo := math.Max(float64(i)-0.5, uj-0.5*scale)
	hi := math.Min(float64(i)+0.5, uj+0.5*scale)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// oracleBox is a fully independent brute-force area-overlap reference for the
// box kernel, written directly from the continuous mirror extension.
func oracleBox(t *testing.T, geo Geometry, src Plane) [][]float64 {
	t.Helper()
	inW, inH := geo.Src.Width, geo.Src.Height
	outW, outH := geo.Dst.Width, geo.Dst.Height
	dInX := float64(geo.RefWidth) / float64(inW)
	dInY := float64(geo.RefHeight) / float64(inH)
	dOutX := float64(geo.RefWidth) / float64(outW)
	dOutY := float64(geo.RefHeight) / float64(outH)
	sx, sy := dOutX/dInX, dOutY/dInY

	out := make([][]float64, outH)
	for y := 0; y < outH; y++ {
		out[y] = make([]float64, outW)
		vy := (float64(y)*dOutY + geo.Dst.CenterY.Float() - geo.Src.CenterY.Float()) / dInY
		for x := 0; x < outW; x++ {
			ux := (float64(x)*dOutX + geo.Dst.CenterX.Float() - geo.Src.CenterX.Float()) / dInX
			var xs []struct {
				index  int
				weight float64
			}
			for ix := int(math.Floor(ux-sx)) - 1; ix <= int(math.Ceil(ux+sx))+1; ix++ {
				if w := overlap1(ix, ux, sx); w > 0 {
					xs = append(xs, struct {
						index  int
						weight float64
					}{ix, w})
				}
			}
			var ys []struct {
				index  int
				weight float64
			}
			for iy := int(math.Floor(vy-sy)) - 1; iy <= int(math.Ceil(vy+sy))+1; iy++ {
				if w := overlap1(iy, vy, sy); w > 0 {
					ys = append(ys, struct {
						index  int
						weight float64
					}{iy, w})
				}
			}
			acc := 0.0
			wsum := 0.0
			for _, ty := range ys {
				for _, tx := range xs {
					mx, my := mirrorIndex(tx.index, inW), mirrorIndex(ty.index, inH)
					w := tx.weight * ty.weight
					acc += w * float64(src.Pix[my*src.Stride+mx])
					wsum += w
				}
			}
			out[y][x] = acc / wsum
		}
	}
	return out
}

func maxAbsDiff(a, b [][]float64) float64 {
	d := 0.0
	for y := range a {
		for x := range a[y] {
			if v := math.Abs(a[y][x] - b[y][x]); v > d {
				d = v
			}
		}
	}
	return d
}

// TestRampAnalytic verifies exact position handling with an analytic linear
// ramp. Box area weighting of a linear function reproduces the analytic value
// at the output sampling center wherever the filter footprint stays inside
// the source extent, for every 4:4:4/4:2:2/4:2:0 pairing and odd dimensions.
func TestRampAnalytic(t *testing.T) {
	const refW, refH = 9, 7 // odd width and height
	formats := mpegFormats(refW, refH)

	ramp := func(x, y int) uint8 {
		return uint8(3*float64(x) + 2*float64(y) + 4)
	}

	for srcName, srcFmt := range formats {
		for dstName, dstFmt := range formats {
			geo := geometryFor(refW, refH, srcFmt, dstFmt)
			src := makePlane(geo.Src, 0)
			fillPlane(src, ramp)
			r, err := New(geo, BoxKernel{})
			if err != nil {
				t.Fatalf("New(%s->%s): %v", srcName, dstName, err)
			}
			got, err := r.RelocateFloat(src)
			if err != nil {
				t.Fatalf("RelocateFloat(%s->%s): %v", srcName, dstName, err)
			}

			// The analytic truth for a piecewise-linear ramp under box area
			// weighting is the independent area-overlap reference.
			want := oracleBox(t, geo, src)
			if d := maxAbsDiff(got, want); d > 1e-9 {
				t.Fatalf("%s->%s ramp vs analytic area oracle: %.3g", srcName, dstName, d)
			}
		}
	}
}

// TestRampAnalytic1D uses a strictly interior, whole-cell downscale where the
// ramp's area average has a closed form: averaging 3*k+4 over source indices
// [lo, hi] is 3*(lo+hi)/2 + 4, i.e. its value at the footprint midpoint.
func TestRampAnalytic1D(t *testing.T) {
	const refW, refH = 12, 10 // even reference so interior cells line up
	formats := mpegFormats(refW, refH)
	srcFmt, dstFmt := formats["444"], formats["422"]
	geo := geometryFor(refW, refH, srcFmt, dstFmt)
	src := makePlane(geo.Src, 0)
	fillPlane(src, func(x, y int) uint8 { return uint8(3*x + 4) })
	r, err := New(geo, BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.RelocateFloat(src)
	if err != nil {
		t.Fatal(err)
	}
	// Output chroma sample x covers whole source cells 2x and 2x+1 (MPEG
	// center 1/2); their mean is 3*(2x+0.5) + 4 = 6x + 5.5, away from edges.
	for x := 1; x < dstFmt.w-1; x++ {
		want := 6*float64(x) + 5.5
		if math.Abs(got[0][x]-want) > 1e-9 {
			t.Fatalf("interior 422 ramp x=%d: got %.12f want %.12f", x, got[0][x], want)
		}
	}
}

// TestColorCheckerboard exercises the half-pixel chroma siting explicitly:
// MPEG 4:2:0 chroma centered at (1/2,1/2) averages the four surrounding
// luma-phase samples, while co-sited JPEG chroma at (0,0) must not.
func TestColorCheckerboard(t *testing.T) {
	const refW, refH = 8, 8
	mpeg := mpegFormats(refW, refH)["420"]
	jpeg := jpegFormats(refW, refH)["420"]
	srcFmt := format{"444", refW, refH, Rational{0, 1}, Rational{0, 1}}

	src := makePlane(gridOf(srcFmt), 0)
	fillPlane(src, func(x, y int) uint8 {
		if (x+y)%2 == 0 {
			return 0
		}
		return 255
	})

	// MPEG: each interior chroma cell covers two black and two white luma
	// cells, hence 127.5, which rounds half-to-even up to 128.
	geoMpeg := geometryFor(refW, refH, srcFmt, mpeg)
	r, err := New(geoMpeg, BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	dstMpeg := makePlane(geoMpeg.Dst, 0)
	if err := r.Relocate(dstMpeg, src); err != nil {
		t.Fatal(err)
	}
	floats, err := r.RelocateFloat(src)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < mpeg.h; y++ {
		for x := 0; x < mpeg.w; x++ {
			if x > 0 && y > 0 {
				if math.Abs(floats[y][x]-127.5) > 1e-9 {
					t.Fatalf("mpeg 420 float (%d,%d) = %v, want 127.5", x, y, floats[y][x])
				}
				if dstMpeg.Pix[y*dstMpeg.Stride+x] != 128 {
					t.Fatalf("mpeg 420 (%d,%d) = %d, want half-even 128",
						x, y, dstMpeg.Pix[y*dstMpeg.Stride+x])
				}
			}
			if v := dstMpeg.Pix[y*dstMpeg.Stride+x]; v > 255 {
				t.Fatalf("value out of uint8 range: %d", v)
			}
		}
	}

	// JPEG co-sited on vertically striped data: the origin chroma cell is
	// anchored on the black column. With mirror boundaries the reflected
	// neighbor is also black, so the whole cell is 0, distinct from MPEG 128.
	geoJpeg := geometryFor(refW, refH, srcFmt, jpeg)
	rj, err := New(geoJpeg, BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	dstJpeg := makePlane(geoJpeg.Dst, 0)
	stripe := makePlane(gridOf(srcFmt), 0)
	fillPlane(stripe, func(x, y int) uint8 {
		if x < 2 {
			return 0
		}
		return 255
	})
	if err := rj.Relocate(dstJpeg, stripe); err != nil {
		t.Fatal(err)
	}
	if got := dstJpeg.Pix[0]; got != 0 {
		t.Fatalf("jpeg co-sited chroma origin = %d, want 0 (centers must not be assumed equal)", got)
	}
}

// TestSaturation keeps every output inside the sample range even when the
// reconstruction overshoots (Lanczos ringing on a hard black/white edge).
func TestSaturation(t *testing.T) {
	const n = 16
	g := Grid{n, n, Rational{0, 1}, Rational{0, 1}}
	src := makePlane(g, 0)
	fillPlane(src, func(x, y int) uint8 {
		if x < n/2 {
			return 0
		}
		return 255
	})
	geo := Geometry{RefWidth: n, RefHeight: n, Src: g, Dst: g}
	r, err := New(geo, LanczosKernel{Taps: 3})
	if err != nil {
		t.Fatal(err)
	}
	dst := makePlane(g, 0)
	if err := r.Relocate(dst, src); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if v := dst.Pix[y*dst.Stride+x]; v > 255 {
				t.Fatalf("unsaturated value %d at (%d,%d)", v, x, y)
			}
		}
	}
}

func gridOf(f format) Grid {
	return Grid{f.w, f.h, f.centerX, f.centerY}
}

// TestConstantsPreserved requires normalized weights to reproduce any flat
// field exactly, for every kernel, format and odd dimension.
func TestConstantsPreserved(t *testing.T) {
	kernels := []Kernel{BoxKernel{}, TriangleKernel{}, LanczosKernel{Taps: 2}, LanczosKernel{Taps: 3}}
	for _, dims := range [][2]int{{8, 8}, {9, 7}, {5, 3}} {
		refW, refH := dims[0], dims[1]
		formats := mpegFormats(refW, refH)
		for srcName, srcFmt := range formats {
			for dstName, dstFmt := range formats {
				for _, k := range kernels {
					geo := geometryFor(refW, refH, srcFmt, dstFmt)
					src := makePlane(geo.Src, 0)
					fillPlane(src, func(x, y int) uint8 { return 200 })
					r, err := New(geo, k)
					if err != nil {
						t.Fatal(err)
					}
					got, err := r.RelocateFloat(src)
					if err != nil {
						t.Fatal(err)
					}
					for y := range got {
						for x := range got[y] {
							if math.Abs(got[y][x]-200) > 1e-9 {
								t.Fatalf("kernel %T %s->%s (%dx%d) at (%d,%d): %v != 200",
									k, srcName, dstName, refW, refH, x, y, got[y][x])
							}
						}
					}
				}
			}
		}
	}
}

// TestIdentityAndRoundTrip checks same-grid box identity and a
// 4:4:4 -> 4:2:0 -> 4:4:4 round trip with explicit half-pixel centers.
func TestIdentityAndRoundTrip(t *testing.T) {
	const refW, refH = 9, 7
	formats := mpegFormats(refW, refH)
	f444, f420 := formats["444"], formats["420"]

	src := makePlane(gridOf(f444), 1)
	fillPlane(src, func(x, y int) uint8 { return uint8((x*13 + y*7) & 0xff) })

	id, err := New(geometryFor(refW, refH, f444, f444), BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	identity := makePlane(gridOf(f444), 1)
	if err := id.Relocate(identity, src); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < refH; y++ {
		for x := 0; x < refW; x++ {
			if identity.Pix[y*identity.Stride+x] != src.Pix[y*src.Stride+x] {
				t.Fatalf("box identity changed sample at (%d,%d)", x, y)
			}
		}
	}

	down, err := New(geometryFor(refW, refH, f444, f420), BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	mid := makePlane(down.Geometry().Dst, 0)
	if err := down.Relocate(mid, src); err != nil {
		t.Fatal(err)
	}
	up, err := New(geometryFor(refW, refH, f420, f444), BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	back := makePlane(gridOf(f444), 0)
	if err := up.Relocate(back, mid); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < refH; y++ {
		for x := 0; x < refW; x++ {
			if v := back.Pix[y*back.Stride+x]; v > 255 {
				t.Fatalf("round trip out of range: %d", v)
			}
		}
	}
}

// TestExplicitCenterMovesSamples proves the center is a real positional
// parameter: identical data resampled with MPEG (1/2) versus JPEG (0) chroma
// centers must produce different interior chroma values.
func TestExplicitCenterMovesSamples(t *testing.T) {
	const refW, refH = 8, 4
	srcFmt := format{"444", refW, refH, Rational{0, 1}, Rational{0, 1}}
	src := makePlane(gridOf(srcFmt), 0)
	fillPlane(src, func(x, y int) uint8 {
		if x%2 == 0 {
			return 16
		}
		return 240
	})

	mpeg := mpegFormats(refW, refH)["422"]
	jpeg := jpegFormats(refW, refH)["422"]

	rm, err := New(geometryFor(refW, refH, srcFmt, mpeg), BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	rj, err := New(geometryFor(refW, refH, srcFmt, jpeg), BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	outM := makePlane(rm.Geometry().Dst, 0)
	outJ := makePlane(rj.Geometry().Dst, 0)
	if err := rm.Relocate(outM, src); err != nil {
		t.Fatal(err)
	}
	if err := rj.Relocate(outJ, src); err != nil {
		t.Fatal(err)
	}
	if outM.Pix[0] == outJ.Pix[0] {
		t.Fatalf("mpeg and jpeg chroma centers produced identical value %d", outM.Pix[0])
	}
	// MPEG chroma sample 0 straddles 16 and 240: mean 128 exactly.
	if got := outM.Pix[0]; got != 128 {
		t.Fatalf("mpeg half-pixel chroma = %d, want 128", got)
	}
	// JPEG chroma cell 0 is anchored on the 16 sample and only its right
	// quarter reaches the 240 neighbor: 3/4*16 + 1/4*240 = 72.
	if got := outJ.Pix[0]; got != 72 {
		t.Fatalf("jpeg co-sited chroma = %d, want 72", got)
	}
}

// TestRelocatePlanes drives both chroma planes together through the YUV
// helper with distinct Cb/Cr contents and odd dimensions.
func TestRelocatePlanes(t *testing.T) {
	const refW, refH = 9, 7
	f444 := mpegFormats(refW, refH)["444"]
	f420 := mpegFormats(refW, refH)["420"]
	geo := geometryFor(refW, refH, f444, f420)

	srcCb := makePlane(gridOf(f444), 0)
	srcCr := makePlane(gridOf(f444), 0)
	fillPlane(srcCb, func(x, y int) uint8 { return uint8((x + y) & 0xff) })
	fillPlane(srcCr, func(x, y int) uint8 { return uint8((2*x + 3*y + 50) & 0xff) })
	dstCb := makePlane(geo.Dst, 1)
	dstCr := makePlane(geo.Dst, 1)

	if err := RelocatePlanes(dstCb, srcCb, dstCr, srcCr, geo, BoxKernel{}); err != nil {
		t.Fatal(err)
	}
	r, err := New(geo, BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	wantCb, err := r.RelocateFloat(srcCb)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < geo.Dst.Height; y++ {
		for x := 0; x < geo.Dst.Width; x++ {
			if got := dstCb.Pix[y*dstCb.Stride+x]; got != saturate8(roundEven(wantCb[y][x])) {
				t.Fatalf("cb (%d,%d) = %d, want %.0f", x, y, got, roundEven(wantCb[y][x]))
			}
			if v := dstCr.Pix[y*dstCr.Stride+x]; v > 255 {
				t.Fatalf("cr out of range %d", v)
			}
		}
	}
}

// TestValidation rejects malformed geometry and planes; a missing center
// (zero Rational with Den == 0) is an error rather than a silent default.
func TestValidation(t *testing.T) {
	good := Grid{4, 4, Rational{1, 2}, Rational{1, 2}}
	noCenter := Grid{4, 4, Rational{}, Rational{0, 1}}
	badGeometries := []Geometry{
		{RefWidth: 0, RefHeight: 4, Src: good, Dst: good},
		{RefWidth: 4, RefHeight: 4, Src: Grid{0, 4, Rational{0, 1}, Rational{0, 1}}, Dst: good},
		{RefWidth: 4, RefHeight: 4, Src: noCenter, Dst: good},
		{RefWidth: 4, RefHeight: 4, Src: Grid{4, 4, Rational{1, 0}, Rational{0, 1}}, Dst: good},
	}
	for _, geo := range badGeometries {
		if _, err := New(geo, BoxKernel{}); err == nil {
			t.Fatalf("expected error for geometry %+v", geo)
		}
	}
	if _, err := New(Geometry{RefWidth: 4, RefHeight: 4, Src: good, Dst: good}, nil); err == nil {
		t.Fatal("expected error for nil kernel")
	}

	geo := Geometry{RefWidth: 4, RefHeight: 4, Src: good, Dst: good}
	r, err := New(geo, BoxKernel{})
	if err != nil {
		t.Fatal(err)
	}
	short := Plane{Grid: good, Pix: make([]uint8, 2), Stride: 4}
	dst := makePlane(good, 0)
	if err := r.Relocate(dst, short); err == nil {
		t.Fatal("expected error for undersized source plane")
	}
	wrongGrid := makePlane(Grid{2, 2, Rational{0, 1}, Rational{0, 1}}, 0)
	if err := r.Relocate(wrongGrid, makePlane(good, 0)); err == nil {
		t.Fatal("expected error for mismatched destination grid")
	}
}

// TestBoxMatchesIndependentOracle checks the whole image, including mirrored
// boundaries and padded strides, against an independently written area-overlap
// reference, and checks the uint8 output equals half-even rounding of it.
func TestBoxMatchesIndependentOracle(t *testing.T) {
	for _, dims := range [][2]int{{8, 8}, {9, 7}, {11, 5}, {1, 1}, {3, 1}} {
		refW, refH := dims[0], dims[1]
		formats := mpegFormats(refW, refH)
		for srcName, srcFmt := range formats {
			for dstName, dstFmt := range formats {
				geo := geometryFor(refW, refH, srcFmt, dstFmt)
				src := makePlane(geo.Src, 2)
				fillPlane(src, func(x, y int) uint8 {
					return uint8((x*37 + y*17 + 11) & 0xff)
				})
				r, err := New(geo, BoxKernel{})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				got, err := r.RelocateFloat(src)
				if err != nil {
					t.Fatalf("RelocateFloat: %v", err)
				}
				want := oracleBox(t, geo, src)
				if d := maxAbsDiff(got, want); d > 1e-10 {
					t.Fatalf("%dx%d %s->%s oracle mismatch %.3g", refW, refH, srcName, dstName, d)
				}

				dst := makePlane(geo.Dst, 3)
				if err := r.Relocate(dst, src); err != nil {
					t.Fatalf("Relocate: %v", err)
				}
				for y := range want {
					for x := range want[y] {
						want8 := saturate8(roundEven(got[y][x]))
						if got8 := dst.Pix[y*dst.Stride+x]; got8 != want8 {
							t.Fatalf("%s->%s uint8 at (%d,%d): got %d want %d",
								srcName, dstName, x, y, got8, want8)
						}
					}
				}
			}
		}
	}
}
