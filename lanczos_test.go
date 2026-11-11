package linscale

import "testing"

func TestKernelWideningOnDownscale(t *testing.T) {
	// At scale 1 the kernel spans 6 source samples (ceil(-3)..floor(3)).
	up := buildAxis(100, 200)
	upTaps := up.dstStart[1] - up.dstStart[0]
	if upTaps != 6 {
		t.Fatalf("upscale kernel width = %d, want 6", upTaps)
	}

	// Downscaling 2:1 widens support to lanczosA/scale = 6, i.e. 12 taps.
	down := buildAxis(100, 50)
	downTaps := down.dstStart[1] - down.dstStart[0]
	if downTaps != 12 {
		t.Fatalf("downscale kernel width = %d, want 12", downTaps)
	}

	// Every destination sample's weights must still sum to one after the
	// edge-clamp normalization.
	for i := 0; i < 50; i++ {
		var sum float64
		for _, w := range down.weights[down.dstStart[i]:down.dstStart[i+1]] {
			sum += float64(w)
		}
		if d := sum - 1; d > 1e-6 || d < -1e-6 {
			t.Fatalf("weights at dst %d sum to %.8f, want 1", i, sum)
		}
	}
}

func TestClampIndex(t *testing.T) {
	if clampIndex(-5, 10) != 0 || clampIndex(0, 10) != 0 {
		t.Fatal("low clamp broken")
	}
	if clampIndex(10, 10) != 9 || clampIndex(99, 10) != 9 {
		t.Fatal("high clamp broken")
	}
	if clampIndex(4, 10) != 4 {
		t.Fatal("in-range clamp broken")
	}
}
