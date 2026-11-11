package yuvconv

import (
	"image"
	"math"
	"testing"
)

// TestSMPTEBarsKnownValues checks every supported decoder against the known
// BT.601 limited-range color-bar code values.
func TestSMPTEBarsKnownValues(t *testing.T) {
	const w, h = 16, 8
	cw, ch := (w+1)/2, (h+1)/2
	i420 := buildI420(w, h, smpteBars)
	nv12 := i420ToNV12(i420, cw, ch)
	p010 := i420ToP010(i420, w, h, cw, ch)

	params := Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaLeft}

	outs := map[string]*image.RGBA{}

	got, err := I420(i420, params)
	if err != nil {
		t.Fatalf("I420: %v", err)
	}
	outs["I420"] = got

	got, err = NV12(nv12, params)
	if err != nil {
		t.Fatalf("NV12: %v", err)
	}
	outs["NV12"] = got

	got, err = P010(p010, params)
	if err != nil {
		t.Fatalf("P010: %v", err)
	}
	outs["P010"] = got

	for name, img := range outs {
		// Quantized 8/10-bit inputs plus rounded reference code values leave a
		// few code values of slack; anything larger means a matrix/range bug.
		if d := maxAbsDiff(img, w, h, barRGB); d > 3 {
			t.Errorf("%s: max color-bar error = %d, want <= 3", name, d)
		}
	}

	// I420 and NV12 carry identical data and must decode pixel-identically;
	// P010 only differs by its 10-bit quantization.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := outs["I420"].PixOffset(x, y)
			n := outs["NV12"].PixOffset(x, y)
			p := outs["P010"].PixOffset(x, y)
			for k := 0; k < 3; k++ {
				if outs["I420"].Pix[i+k] != outs["NV12"].Pix[n+k] {
					t.Fatalf("I420/NV12 mismatch at (%d,%d) channel %d", x, y, k)
				}
				if absInt(int(outs["I420"].Pix[i+k])-int(outs["P010"].Pix[p+k])) > 1 {
					t.Fatalf("I420/P010 mismatch >1 at (%d,%d) channel %d", x, y, k)
				}
			}
		}
	}
}

// encodeRGB builds an I420 frame from an sRGB image using the given matrix
// and range, so round trips can be checked without external fixtures.
func encodeRGB(img *image.RGBA, m Matrix, rng Range) I420Frame {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	cw, ch := (w+1)/2, (h+1)/2
	out := I420Frame{Y: make([]byte, w*h), U: make([]byte, cw*ch), V: make([]byte, cw*ch)}
	kr, kb := matrixWeights(m)
	kg := 1 - kr - kb

	yLow, yHigh, cLow, cHigh := 0.0, 255.0, 0.0, 255.0
	if rng == RangeLimited {
		yLow, yHigh = 16, 235
		cLow, cHigh = 16, 240
	}
	encode := func(v, low, high float64) uint8 {
		code := low + v*(high-low)
		if code < low {
			code = low
		}
		if code > high {
			code = high
		}
		return uint8(math.Round(code))
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			rf := float64(img.Pix[i]) / 255
			gf := float64(img.Pix[i+1]) / 255
			bf := float64(img.Pix[i+2]) / 255
			yn := kr*rf + kg*gf + kb*bf
			out.Y[y*w+x] = encode(yn, yLow, yHigh)
		}
	}

	// Average each 2x2 luma block; on odd widths/heights the last chroma
	// sample reuses the border pixel instead of reading past the frame.
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			x0 := cx * 2
			y0 := cy * 2
			x1 := min(x0+1, w-1)
			y1 := min(y0+1, h-1)
			var cbSum, crSum float64
			for yy := y0; yy <= y1; yy++ {
				for xx := x0; xx <= x1; xx++ {
					i := img.PixOffset(xx, yy)
					rf := float64(img.Pix[i]) / 255
					gf := float64(img.Pix[i+1]) / 255
					bf := float64(img.Pix[i+2]) / 255
					yn := kr*rf + kg*gf + kb*bf
					cbSum += (bf-yn)/(2*(1-kb)) + 0.5
					crSum += (rf-yn)/(2*(1-kr)) + 0.5
				}
			}
			n := float64((x1 - x0 + 1) * (y1 - y0 + 1))
			out.U[cy*cw+cx] = encode(cbSum/n, cLow, cHigh)
			out.V[cy*cw+cx] = encode(crSum/n, cLow, cHigh)
		}
	}
	return out
}

// TestRoundTripAllMatricesRanges verifies matrix and range handling. The
// source uses solid 2x2 color blocks, which 4:2:0 chroma can represent
// exactly, so any large error points at a matrix or range bug rather than at
// subsampling.
func TestRoundTripAllMatricesRanges(t *testing.T) {
	const w, h = 8, 8
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := src.PixOffset(x, y)
			block := (y/2)*4 + x/2
			// Keep the blocks low-saturation so 8-bit chroma quantization
			// stays tight; the point of the test is matrix/range correctness.
			base := uint8(40 + block*22)
			src.Pix[i+0] = base
			src.Pix[i+1] = base + uint8(block%7)
			src.Pix[i+2] = base + uint8((block*2)%11)
			src.Pix[i+3] = 255
		}
	}

	matrices := []Matrix{MatrixBT601, MatrixBT709, MatrixBT2020}
	ranges := []Range{RangeLimited, RangeFull}
	for _, m := range matrices {
		for _, rng := range ranges {
			frame := encodeRGB(src, m, rng)
			p := Params{Width: w, Height: h, Matrix: m, Range: rng, Chroma: ChromaLeft}
			got, err := I420(frame, p)
			if err != nil {
				t.Fatalf("matrix %v range %v: %v", m, rng, err)
			}
			want := func(x, y int) (uint8, uint8, uint8) {
				i := src.PixOffset(x, y)
				return src.Pix[i], src.Pix[i+1], src.Pix[i+2]
			}
			tol := 6
			if rng == RangeFull {
				tol = 5
			}
			if d := maxAbsDiff(got, w, h, want); d > tol {
				t.Errorf("matrix %v range %v: round-trip error %d > %d", m, rng, d, tol)
			}
		}
	}
}
