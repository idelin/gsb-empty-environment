package jpegrotate

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// sampleYCbCr builds a deterministic test image with structured content
// plus noise, so that DCT coefficients are non-trivial.
func sampleYCbCr(w, h int, ratio image.YCbCrSubsampleRatio) *image.YCbCr {
	m := image.NewYCbCr(image.Rect(0, 0, w, h), ratio)
	n := uint32(1)
	rnd := func() uint32 {
		n = n*1664525 + 1013904223
		return n >> 24
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Y[y*m.YStride+x] = uint8(x*3+y*5) ^ uint8(rnd()&0x1F)
		}
	}
	cw, ch := chromaSize(w, h, ratio)
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			m.Cb[y*m.CStride+x] = uint8(128+x*2-y*3) ^ uint8(rnd()&0x0F)
			m.Cr[y*m.CStride+x] = uint8(200-x*4+y) ^ uint8(rnd()&0x0F)
		}
	}
	return m
}

func chromaSize(w, h int, ratio image.YCbCrSubsampleRatio) (int, int) {
	switch ratio {
	case image.YCbCrSubsampleRatio444:
		return w, h
	case image.YCbCrSubsampleRatio420:
		return (w + 1) / 2, (h + 1) / 2
	}
	panic("unsupported ratio")
}

func encodeJPEG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	m, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Pixel-space reference transforms, applied plane by plane.

type planeFunc func(src []byte, stride, w, h int) ([]byte, int)

func planeRot90(src []byte, stride, w, h int) ([]byte, int) {
	dst := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[x*h+(h-1-y)] = src[y*stride+x]
		}
	}
	return dst, h
}

func planeRot270(src []byte, stride, w, h int) ([]byte, int) {
	dst := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[(w-1-x)*h+y] = src[y*stride+x]
		}
	}
	return dst, h
}

func planeRot180(src []byte, stride, w, h int) ([]byte, int) {
	dst := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[(h-1-y)*w+(w-1-x)] = src[y*stride+x]
		}
	}
	return dst, w
}

func planeFlipH(src []byte, stride, w, h int) ([]byte, int) {
	dst := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[y*w+(w-1-x)] = src[y*stride+x]
		}
	}
	return dst, w
}

func planeFlipV(src []byte, stride, w, h int) ([]byte, int) {
	dst := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[(h-1-y)*w+x] = src[y*stride+x]
		}
	}
	return dst, w
}

var pixelFuncs = map[Transform]planeFunc{
	Rot90:  planeRot90,
	Rot180: planeRot180,
	Rot270: planeRot270,
	FlipH:  planeFlipH,
	FlipV:  planeFlipV,
}

// transformPixels rotates/mirrors a decoded YCbCr image in the pixel
// domain. Only symmetric subsampling ratios (4:4:4, 4:2:0) are supported.
func transformPixels(m *image.YCbCr, t Transform) *image.YCbCr {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	cw, ch := chromaSize(w, h, m.SubsampleRatio)
	f := pixelFuncs[t]
	y, ys := f(m.Y, m.YStride, w, h)
	cb, cs := f(m.Cb, m.CStride, cw, ch)
	cr, _ := f(m.Cr, m.CStride, cw, ch)
	nw, nh := w, h
	if t == Rot90 || t == Rot270 {
		nw, nh = h, w
	}
	return &image.YCbCr{
		Y:              y,
		Cb:             cb,
		Cr:             cr,
		YStride:        ys,
		CStride:        cs,
		SubsampleRatio: m.SubsampleRatio,
		Rect:           image.Rect(0, 0, nw, nh),
	}
}

func planeMaxDiff(a []byte, as int, b []byte, bs, w, h int) int {
	max := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := int(a[y*as+x]) - int(b[y*bs+x])
			if d < 0 {
				d = -d
			}
			if d > max {
				max = d
			}
		}
	}
	return max
}

func assertYCbCrClose(t *testing.T, got, want *image.YCbCr, tol int) {
	t.Helper()
	if got.Rect != want.Rect {
		t.Fatalf("bounds mismatch: got %v, want %v", got.Rect, want.Rect)
	}
	if got.SubsampleRatio != want.SubsampleRatio {
		t.Fatalf("subsample ratio mismatch: got %v, want %v", got.SubsampleRatio, want.SubsampleRatio)
	}
	w, h := got.Rect.Dx(), got.Rect.Dy()
	cw, ch := chromaSize(w, h, got.SubsampleRatio)
	if d := planeMaxDiff(got.Y, got.YStride, want.Y, want.YStride, w, h); d > tol {
		t.Errorf("Y plane max diff %d exceeds tolerance %d", d, tol)
	}
	if d := planeMaxDiff(got.Cb, got.CStride, want.Cb, want.CStride, cw, ch); d > tol {
		t.Errorf("Cb plane max diff %d exceeds tolerance %d", d, tol)
	}
	if d := planeMaxDiff(got.Cr, got.CStride, want.Cr, want.CStride, cw, ch); d > tol {
		t.Errorf("Cr plane max diff %d exceeds tolerance %d", d, tol)
	}
}

func TestLosslessTransforms(t *testing.T) {
	ratios := []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444,
		image.YCbCrSubsampleRatio420,
	}
	transforms := []Transform{Rot90, Rot180, Rot270, FlipH, FlipV}
	sizes := [][2]int{{64, 48}, {48, 64}, {16, 16}}
	for _, ratio := range ratios {
		for _, sz := range sizes {
			src := sampleYCbCr(sz[0], sz[1], ratio)
			data := encodeJPEG(t, src)
			orig := decodeJPEG(t, data).(*image.YCbCr)
			for _, tf := range transforms {
				name := tf.String()
				t.Run(name, func(t *testing.T) {
					var out bytes.Buffer
					if err := TransformJPEG(&out, bytes.NewReader(data), tf, Options{}); err != nil {
						t.Fatal(err)
					}
					got := decodeJPEG(t, out.Bytes()).(*image.YCbCr)
					want := transformPixels(orig, tf)
					// The DCT-domain transform is exact; allow only
					// fixed-point IDCT rounding differences.
					assertYCbCrClose(t, got, want, 2)
				})
			}
		}
	}
}

func TestTrimPartialMCUs(t *testing.T) {
	// 70x52 with 4:2:0 (16x16 MCUs) has partial MCUs on the right and
	// bottom edges; trimming crops to 64x48.
	src := sampleYCbCr(70, 52, image.YCbCrSubsampleRatio420)
	data := encodeJPEG(t, src)
	orig := decodeJPEG(t, data).(*image.YCbCr)

	if err := TransformJPEG(&bytes.Buffer{}, bytes.NewReader(data), Rot90, Options{}); err == nil {
		t.Fatal("expected error for non-multiple-of-MCU dimensions without Trim")
	}

	var out bytes.Buffer
	if err := TransformJPEG(&out, bytes.NewReader(data), Rot90, Options{Trim: true}); err != nil {
		t.Fatal(err)
	}
	got := decodeJPEG(t, out.Bytes()).(*image.YCbCr)
	if got.Rect.Dx() != 48 || got.Rect.Dy() != 64 {
		t.Fatalf("unexpected trimmed size %v", got.Rect)
	}
	cropped := cropYCbCr(orig, 64, 48)
	want := transformPixels(cropped, Rot90)
	assertYCbCrClose(t, got, want, 2)
}

func cropYCbCr(m *image.YCbCr, w, h int) *image.YCbCr {
	cw, ch := chromaSize(w, h, m.SubsampleRatio)
	out := image.NewYCbCr(image.Rect(0, 0, w, h), m.SubsampleRatio)
	for y := 0; y < h; y++ {
		copy(out.Y[y*out.YStride:y*out.YStride+w], m.Y[y*m.YStride:y*m.YStride+w])
	}
	for y := 0; y < ch; y++ {
		copy(out.Cb[y*out.CStride:y*out.CStride+cw], m.Cb[y*m.CStride:y*m.CStride+cw])
		copy(out.Cr[y*out.CStride:y*out.CStride+cw], m.Cr[y*m.CStride:y*m.CStride+cw])
	}
	return out
}

func TestRot90FourTimesIsIdentity(t *testing.T) {
	src := sampleYCbCr(64, 48, image.YCbCrSubsampleRatio420)
	data := encodeJPEG(t, src)
	cur := data
	for i := 0; i < 4; i++ {
		var out bytes.Buffer
		if err := TransformJPEG(&out, bytes.NewReader(cur), Rot90, Options{}); err != nil {
			t.Fatal(err)
		}
		cur = out.Bytes()
	}
	got := decodeJPEG(t, cur).(*image.YCbCr)
	want := decodeJPEG(t, data).(*image.YCbCr)
	// Coefficient transforms are exact involutions, so the pixels must
	// match the original decode bit for bit.
	assertYCbCrClose(t, got, want, 0)
}

func TestGrayscale(t *testing.T) {
	const w, h = 64, 48
	g := image.NewGray(image.Rect(0, 0, w, h))
	n := uint32(7)
	for i := range g.Pix {
		n = n*1664525 + 1013904223
		g.Pix[i] = uint8(n>>24) ^ uint8(i)
	}
	data := encodeJPEG(t, g)
	var out bytes.Buffer
	if err := TransformJPEG(&out, bytes.NewReader(data), Rot270, Options{}); err != nil {
		t.Fatal(err)
	}
	got := decodeJPEG(t, out.Bytes()).(*image.Gray)
	orig := decodeJPEG(t, data).(*image.Gray)
	want, ws := planeRot270(orig.Pix, orig.Stride, w, h)
	if got.Rect.Dx() != h || got.Rect.Dy() != w {
		t.Fatalf("unexpected size %v", got.Rect)
	}
	if d := planeMaxDiff(got.Pix, got.Stride, want, ws, h, w); d > 2 {
		t.Fatalf("gray plane max diff %d exceeds tolerance", d)
	}
}

func TestRestartInterval(t *testing.T) {
	src := sampleYCbCr(64, 48, image.YCbCrSubsampleRatio420)
	data := encodeJPEG(t, src)

	// Re-encode with a restart interval using the package internals.
	j, err := parseJPEG(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	j.restartInterval = 3
	var withRST bytes.Buffer
	if err := j.encode(&withRST); err != nil {
		t.Fatal(err)
	}

	// The standard library must decode the RST stream identically.
	a := decodeJPEG(t, withRST.Bytes()).(*image.YCbCr)
	b := decodeJPEG(t, data).(*image.YCbCr)
	assertYCbCrClose(t, a, b, 0)

	// And transforms of the RST stream must match pixel-space rotation.
	var out bytes.Buffer
	if err := TransformJPEG(&out, bytes.NewReader(withRST.Bytes()), Rot180, Options{}); err != nil {
		t.Fatal(err)
	}
	got := decodeJPEG(t, out.Bytes()).(*image.YCbCr)
	want := transformPixels(b, Rot180)
	assertYCbCrClose(t, got, want, 2)
}

func TestRejectsNonBaseline(t *testing.T) {
	if err := TransformJPEG(&bytes.Buffer{}, bytes.NewReader([]byte("not a jpeg")), Rot90, Options{}); err == nil {
		t.Fatal("expected error for garbage input")
	}
}
