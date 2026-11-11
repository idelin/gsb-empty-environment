package yuvconv

import "testing"

// TestClamping feeds out-of-gamut code values and checks the output is
// clamped to the 8-bit sRGB range instead of wrapping.
func TestClamping(t *testing.T) {
	const w, h = 2, 2
	extremes := []struct {
		name    string
		y, u, v byte
	}{
		{"below-black", 0, 128, 128},
		{"above-white", 255, 128, 128},
		{"max-chroma", 128, 0, 255},
		{"min-chroma", 128, 255, 0},
	}
	for _, e := range extremes {
		frame := I420Frame{
			Y: []byte{e.y, e.y, e.y, e.y},
			U: []byte{e.u},
			V: []byte{e.v},
		}
		p := Params{Width: w, Height: h, Matrix: MatrixBT709, Range: RangeFull, Chroma: ChromaLeft}
		got, err := I420(frame, p)
		if err != nil {
			t.Fatalf("%s: %v", e.name, err)
		}
		hitBound := false
		for i := 0; i < w*h; i++ {
			for k := 0; k < 3; k++ {
				if v := got.Pix[i*4+k]; v == 0 || v == 255 {
					hitBound = true
				}
			}
			if got.Pix[i*4+3] != 255 {
				t.Errorf("%s: alpha = %d, want 255", e.name, got.Pix[i*4+3])
			}
		}
		if !hitBound {
			t.Errorf("%s: out-of-gamut input never reached a clamped boundary", e.name)
		}
	}

	// Limited range: luma code 1 sits below black and must clamp to 0.
	black := I420Frame{
		Y: []byte{1, 1, 1, 1},
		U: []byte{128},
		V: []byte{128},
	}
	p := Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaLeft}
	got, err := I420(black, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < w*h; i++ {
		for k := 0; k < 3; k++ {
			if got.Pix[i*4+k] != 0 {
				t.Errorf("below-black channel %d = %d, want 0", k, got.Pix[i*4+k])
			}
		}
	}
}

// TestValidation makes malformed inputs fail with ErrInvalidParams rather than
// panicking on out-of-range indexing.
func TestValidation(t *testing.T) {
	good := Params{Width: 4, Height: 4, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaLeft}

	if _, err := I420(I420Frame{}, good); err != ErrInvalidParams {
		t.Errorf("empty I420: err = %v, want ErrInvalidParams", err)
	}

	frame := buildI420(4, 4, smpteBars)
	if _, err := I420(frame, Params{Width: 0, Height: 4}); err != ErrInvalidParams {
		t.Errorf("zero width: err = %v, want ErrInvalidParams", err)
	}
	if _, err := I420(frame, Params{Width: 4, Height: 4, Matrix: Matrix(99)}); err != ErrInvalidParams {
		t.Errorf("bad matrix: err = %v, want ErrInvalidParams", err)
	}
	if _, err := I420(frame, Params{Width: 4, Height: 4, Range: Range(99)}); err != ErrInvalidParams {
		t.Errorf("bad range: err = %v, want ErrInvalidParams", err)
	}
	if _, err := I420(frame, Params{Width: 4, Height: 4, Chroma: ChromaLoc(99)}); err != ErrInvalidParams {
		t.Errorf("bad chroma loc: err = %v, want ErrInvalidParams", err)
	}

	shortY := frame
	shortY.Y = frame.Y[:len(frame.Y)-1]
	if _, err := I420(shortY, good); err != ErrInvalidParams {
		t.Errorf("short Y plane: err = %v, want ErrInvalidParams", err)
	}
	shortU := frame
	shortU.U = frame.U[:1]
	if _, err := I420(shortU, good); err != ErrInvalidParams {
		t.Errorf("short U plane: err = %v, want ErrInvalidParams", err)
	}

	nv := i420ToNV12(frame, 2, 2)
	if _, err := NV12(NV12Frame{Y: nv.Y, C: nv.C[:3]}, good); err != ErrInvalidParams {
		t.Errorf("short NV12 C plane: err = %v, want ErrInvalidParams", err)
	}

	p010 := i420ToP010(frame, 4, 4, 2, 2)
	if _, err := P010(P010Frame{Y: p010.Y, C: p010.C[:3]}, good); err != ErrInvalidParams {
		t.Errorf("short P010 C plane: err = %v, want ErrInvalidParams", err)
	}
	badStride := p010
	badStride.YStride = 1
	if _, err := P010(badStride, good); err != ErrInvalidParams {
		t.Errorf("P010 bad stride: err = %v, want ErrInvalidParams", err)
	}
}
