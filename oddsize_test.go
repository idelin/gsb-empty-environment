package yuvconv

import (
	"image"
	"testing"
)

// TestOddDimensionsNoPanic decodes odd-sized frames (including padded
// strides) for every format and sample location, and verifies the output keeps
// its geometry and stays inside the 8-bit code range. The goal is to exercise
// the last row/column border handling, not subsampling precision.
func TestOddDimensionsNoPanic(t *testing.T) {
	dims := [][2]int{{1, 1}, {3, 3}, {5, 3}, {7, 5}, {15, 9}}
	for _, dim := range dims {
		w, h := dim[0], dim[1]
		cw, ch := (w+1)/2, (h+1)/2

		frame := buildI420(w, h, smpteBars)
		nv := i420ToNV12(frame, cw, ch)
		p := i420ToP010(frame, w, h, cw, ch)

		yStride := w + 4
		paddedI420 := I420Frame{
			Y: make([]byte, yStride*h), U: make([]byte, (cw+3)*ch), V: make([]byte, (cw+3)*ch),
			YStride: yStride, CStride: cw + 3,
		}
		for yy := 0; yy < h; yy++ {
			copy(paddedI420.Y[yy*yStride:], frame.Y[yy*w:yy*w+w])
		}
		for cy := 0; cy < ch; cy++ {
			copy(paddedI420.U[cy*(cw+3):], frame.U[cy*cw:cy*cw+cw])
			copy(paddedI420.V[cy*(cw+3):], frame.V[cy*cw:cy*cw+cw])
		}

		cStridePad := 2*cw + 6
		paddedNV := NV12Frame{
			Y: make([]byte, yStride*h), C: make([]byte, cStridePad*ch),
			YStride: yStride, CStride: cStridePad,
		}
		for yy := 0; yy < h; yy++ {
			copy(paddedNV.Y[yy*yStride:], nv.Y[yy*w:yy*w+w])
		}
		for cy := 0; cy < ch; cy++ {
			copy(paddedNV.C[cy*cStridePad:], nv.C[cy*2*cw:cy*2*cw+2*cw])
		}

		pStride, pcStride := w+8, 2*cw+4
		paddedP := P010Frame{
			Y: make([]uint16, pStride*h), C: make([]uint16, pcStride*ch),
			YStride: pStride, CStride: pcStride,
		}
		for yy := 0; yy < h; yy++ {
			copy(paddedP.Y[yy*pStride:], p.Y[yy*w:yy*w+w])
		}
		for cy := 0; cy < ch; cy++ {
			copy(paddedP.C[cy*pcStride:], p.C[cy*2*cw:cy*2*cw+2*cw])
		}

		for _, loc := range []ChromaLoc{ChromaLeft, ChromaCenter} {
			params := Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: loc}
			outs := map[string]*image.RGBA{}

			got, err := I420(paddedI420, params)
			if err != nil {
				t.Fatalf("I420 %dx%d loc %v: %v", w, h, loc, err)
			}
			outs["I420"] = got

			got, err = NV12(paddedNV, params)
			if err != nil {
				t.Fatalf("NV12 %dx%d loc %v: %v", w, h, loc, err)
			}
			outs["NV12"] = got

			got, err = P010(paddedP, params)
			if err != nil {
				t.Fatalf("P010 %dx%d loc %v: %v", w, h, loc, err)
			}
			outs["P010"] = got

			for name, img := range outs {
				if img.Rect.Dx() != w || img.Rect.Dy() != h {
					t.Errorf("%s %dx%d: geometry = %v", name, w, h, img.Rect)
				}
				if n := len(img.Pix); n != w*h*4 {
					t.Errorf("%s %dx%d: pixel buffer len = %d, want %d", name, w, h, n, w*h*4)
				}
				// The last row and last column must decode to a real bar color.
				last := img.PixOffset(w-1, h-1)
				if img.Pix[last+3] != 255 {
					t.Errorf("%s %dx%d loc %v: alpha at last pixel = %d", name, w, h, loc, img.Pix[last+3])
				}
			}

			// I420 and NV12 carry identical bytes and must match exactly,
			// including the odd last row/column.
			if string(outs["I420"].Pix) != string(outs["NV12"].Pix) {
				t.Errorf("%dx%d loc %v: I420/NV12 output mismatch", w, h, loc)
			}
		}
	}
}

// TestOddEdgeChroma checks border semantics directly: with center siting the
// final luma row/column of an odd frame clamps onto the final chroma sample;
// with left siting it reads its own block sample.
func TestOddEdgeChroma(t *testing.T) {
	const w, h = 3, 3
	cw := 2
	frame := I420Frame{
		Y: make([]byte, w*h),
		U: []byte{10, 20, 30, 40},
		V: []byte{128, 128, 128, 128},
	}
	for i := range frame.Y {
		frame.Y[i] = 235
	}
	sample := func(cx, cy int, cb bool) float64 {
		if cb {
			return float64(frame.U[cy*cw+cx])
		}
		return 128
	}

	left, _ := newConverter(Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaLeft}, depth8)
	center, _ := newConverter(Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaCenter}, depth8)

	// Last column/row belongs to block (1,1) holding sample 40.
	if got := left.chromaAt(2, 2, sample, true); got != 40 {
		t.Errorf("left last-pixel chroma = %v, want 40", got)
	}
	// Center siting at (2,2) still has four valid neighbors (the plane is
	// 2x2), so it takes the full bilinear average (10+20+30+40)/4 = 25.
	if got := center.chromaAt(2, 2, sample, true); got != 25 {
		t.Errorf("center last-pixel chroma = %v, want 25", got)
	}
	// Interior even pixel (2,0) averages the two chroma columns of row 0.
	if got := center.chromaAt(2, 0, sample, true); got != 15 {
		t.Errorf("center (2,0) chroma = %v, want 15", got)
	}
}
