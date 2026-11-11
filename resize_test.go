package linscale

import (
	"image"
	"testing"
)

func TestSRGBRoundTrip(t *testing.T) {
	for v := 0; v < 256; v++ {
		got := int(encodeLinear(decodeTable[v]))
		if got != v {
			t.Fatalf("sRGB round trip at %d: got %d", v, got)
		}
	}
}

func TestIdentity(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := (y*4 + x) * 4
			src.Pix[i+0] = uint8(17 * x)
			src.Pix[i+1] = uint8(64 * y)
			src.Pix[i+2] = uint8(31 * (x + y))
			src.Pix[i+3] = uint8(40 + 30*x)
		}
	}

	dst, err := Resize(src, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := range src.Pix {
		// Lanczos3 is an identity at scale 1: every output tap lands
		// exactly on its matching source sample with weight 1.
		if dst.Pix[i] != src.Pix[i] {
			t.Fatalf("identity mismatch at byte %d: got %d want %d", i, dst.Pix[i], src.Pix[i])
		}
	}
}

// verticalLineImage builds an image whose single white column sits on a black
// background with the given constant alpha.
func verticalLineImage(w, h, lineX int, alpha uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			img.Pix[i+3] = 255
			if x == lineX {
				img.Pix[i+0] = 255
				img.Pix[i+1] = 255
				img.Pix[i+2] = 255
				img.Pix[i+3] = alpha
			}
		}
	}
	return img
}

func TestThinLineLuminance(t *testing.T) {
	// A one-pixel white line downscaled 3:1. The line is offset from the
	// output sample grid so the Lanczos kernel actually spans it; linear
	// filtering must keep the result a neutral grey substantially brighter
	// than filtering the raw sRGB bytes, which collapses toward black.
	src := verticalLineImage(12, 16, 3, 255)
	dst, err := Resize(src, 4, 16)
	if err != nil {
		t.Fatal(err)
	}

	// Output column 1 receives the strongest contribution from the line.
	x := 1
	i := (8*4 + x) * 4
	r, g, b, a := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3]
	if a != 255 {
		t.Fatalf("opaque line lost alpha: %d", a)
	}
	if r != g || g != b {
		t.Fatalf("line not neutral: rgb=(%d,%d,%d)", r, g, b)
	}
	if r < 120 || r > 160 {
		t.Fatalf("line brightness outside expected linear band: sRGB %d", r)
	}

	// Reference: sRGB-space filtering of the same pattern proves it is
	// noticeably darker, i.e. linear filtering is what rescued the line.
	naive := naiveSRGBDownscaleX(src, 4)
	ni := (8*4 + x) * 4
	if naive.Pix[ni] >= r {
		t.Fatalf("naive sRGB filtering (%d) should be darker than linear (%d)",
			naive.Pix[ni], r)
	}
	if r-naive.Pix[ni] < 40 {
		t.Fatalf("linear/naive brightness gap too small: %d vs %d",
			r, naive.Pix[ni])
	}
}

func TestBlackAndWhiteLines(t *testing.T) {
	// Alternating 1 px black/white columns downscaled 4:1. Linear averaging
	// of equal black and white energy yields sRGB 188; sRGB-space averaging
	// collapses toward 127 and visibly darkens the white lines.
	w, h := 16, 8
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			if x%2 == 0 {
				src.Pix[i+0], src.Pix[i+1], src.Pix[i+2] = 255, 255, 255
			}
			src.Pix[i+3] = 255
		}
	}
	dst, err := Resize(src, 4, h)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 4; x++ {
		i := (4*4 + x) * 4
		if dst.Pix[i] != dst.Pix[i+1] || dst.Pix[i+1] != dst.Pix[i+2] {
			t.Fatalf("column %d not neutral: %v", x, dst.Pix[i:i+4])
		}
		if dst.Pix[i] < 175 {
			t.Fatalf("column %d too dark: %d", x, dst.Pix[i])
		}
	}
}

func TestTransparentEdgeNoBlackFringe(t *testing.T) {
	// Opaque red block next to fully transparent pixels. On a straight-alpha
	// (non-premultiplied) filter, transparent black samples dilute the color
	// and the edge turns dark; premultiplied filtering keeps the edge red
	// while only lowering its alpha.
	w, h := 12, 8
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			if x < w/2 {
				src.Pix[i+0], src.Pix[i+1], src.Pix[i+2] = 255, 0, 0
				src.Pix[i+3] = 255
			}
		}
	}

	dst, err := Resize(src, 6, h)
	if err != nil {
		t.Fatal(err)
	}

	for y := 0; y < h; y++ {
		for x := 0; x < 6; x++ {
			i := (y*6 + x) * 4
			r, g, b, a := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3]
			if a == 0 {
				continue
			}
			// Red must dominate everywhere; green/blue staying low proves
			// no black (0,0,0) contamination leaked into the edge color.
			if int(r) <= int(g) || int(r) <= int(b) {
				t.Fatalf("edge pixel (%d,%d) lost red dominance: rgba=(%d,%d,%d,%d)",
					x, y, r, g, b, a)
			}
			if g > 20 || b > 20 {
				t.Fatalf("edge pixel (%d,%d) carries dark fringe: rgba=(%d,%d,%d,%d)",
					x, y, r, g, b, a)
			}
		}
	}

	// The far transparent side is either fully zeroed or only carries an
	// alpha=1 clamp-bleed artifact (never a visible dark/black fringe).
	right := dst.Pix[(4*6+5)*4 : (4*6+5)*4+4]
	if right[3] > 1 || right[1] > 1 || right[2] > 1 {
		t.Fatalf("transparent region contaminated: %v", right)
	}
}

func TestSemiTransparentEdgeColor(t *testing.T) {
	// 50% red next to transparent black, downscaled: unpremultiplied color
	// must remain red (not dark maroon); only alpha may shrink at the edge.
	w, h := 8, 4
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w/2; x++ {
			i := (y*w + x) * 4
			src.Pix[i+0], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 0, 0, 128
		}
	}
	dst, err := Resize(src, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	// Interior column keeps the source color and alpha through the filter.
	i := (2*4 + 0) * 4
	if dst.Pix[i+0] < 250 || dst.Pix[i+1] > 5 || dst.Pix[i+2] > 5 {
		t.Fatalf("interior color shifted: rgba=%v", dst.Pix[i:i+4])
	}
	if dst.Pix[i+3] < 120 || dst.Pix[i+3] > 136 {
		t.Fatalf("interior alpha shifted: %d", dst.Pix[i+3])
	}
}
