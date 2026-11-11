package yuvconv

import "testing"

// TestChromaLocation checks the upsampling weights directly. With center
// siting an interior even-indexed pixel averages two neighboring chroma
// samples, while borders clamp to the only available sample.
func TestChromaLocation(t *testing.T) {
	const w, h = 4, 4
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

	left, err := newConverter(Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaLeft}, depth8)
	if err != nil {
		t.Fatal(err)
	}
	center, err := newConverter(Params{Width: w, Height: h, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaCenter}, depth8)
	if err != nil {
		t.Fatal(err)
	}

	// Left siting: every pixel of a block gets its own block sample.
	if got := left.chromaAt(2, 0, sample, true); got != 20 {
		t.Errorf("left chroma at (2,0) = %v, want 20", got)
	}
	if got := left.chromaAt(3, 3, sample, true); got != 40 {
		t.Errorf("left chroma at (3,3) = %v, want 40", got)
	}

	// Center siting: x=0 clamps to sample 0; x=2 averages samples 0 and 1;
	// the bottom luma row (y=2) averages chroma rows 0 and 1.
	if got := center.chromaAt(0, 0, sample, true); got != 10 {
		t.Errorf("center border chroma at (0,0) = %v, want 10", got)
	}
	if got := center.chromaAt(2, 1, sample, true); got != 15 {
		t.Errorf("center interior chroma at (2,1) = %v, want 15", got)
	}
	if got := center.chromaAt(1, 2, sample, true); got != 20 {
		t.Errorf("center vertical chroma at (1,2) = %v, want 20", got)
	}
	if got := center.chromaAt(2, 2, sample, true); got != 25 {
		t.Errorf("center bilinear chroma at (2,2) = %v, want 25", got)
	}

	// Odd width/height: the last luma column/row must stay inside the plane.
	odd, err := newConverter(Params{Width: 3, Height: 3, Matrix: MatrixBT601, Range: RangeLimited, Chroma: ChromaCenter}, depth8)
	if err != nil {
		t.Fatal(err)
	}
	smallU := []byte{1, 2, 3, 4}
	smallSample := func(cx, cy int, cb bool) float64 {
		if cb {
			return float64(smallU[cy*2+cx])
		}
		return 128
	}
	if got := odd.chromaAt(2, 2, smallSample, true); !(got >= 1 && got <= 4) {
		t.Errorf("odd-frame border chroma = %v, out of sample range [1,4]", got)
	}
	if got := odd.chromaAt(0, 0, smallSample, true); got != 1 {
		t.Errorf("odd-frame top-left chroma = %v, want 1", got)
	}
}
