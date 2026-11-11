package texsample

import (
	"math"
	"math/rand"
	"testing"
)

func approxEqual(a, b, eps float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestMipDimensionsAndCount(t *testing.T) {
	cases := []struct {
		w, h      int
		wantDims  [][2]int
		wantCount int
	}{
		{1, 1, [][2]int{{1, 1}}, 1},
		{4, 4, [][2]int{{4, 4}, {2, 2}, {1, 1}}, 3},
		// Odd, non power of two base size: 5 -> 3 -> 2 -> 1.
		{5, 3, [][2]int{{5, 3}, {3, 2}, {2, 1}, {1, 1}}, 4},
		// Rectangular NPOT where the axes shrink independently.
		{7, 4, [][2]int{{7, 4}, {4, 2}, {2, 1}, {1, 1}}, 4},
		// 3-wide chain shrinks 3 -> 2 -> 1.
		{3, 1, [][2]int{{3, 1}, {2, 1}, {1, 1}}, 3},
	}
	for _, tc := range cases {
		if got := MipLevelCount(tc.w, tc.h); got != tc.wantCount {
			t.Fatalf("MipLevelCount(%d,%d) = %d, want %d", tc.w, tc.h, got, tc.wantCount)
		}
		for l, want := range tc.wantDims {
			gw, gh := MipDimensions(tc.w, tc.h, l)
			if gw != want[0] || gh != want[1] {
				t.Fatalf("MipDimensions(%d,%d,%d) = (%d,%d), want (%d,%d)",
					tc.w, tc.h, l, gw, gh, want[0], want[1])
			}
		}
	}
}

func TestOddMipBoxAveraging(t *testing.T) {
	// 3x1 texture: level 1 is ceil(3/2)=2 texels.
	// Texel 0 covers source [0,1] (mean of 2), texel 1 covers source [2]
	// alone (border texel keeps its value rather than being duplicated).
	src := []float32{
		1, 0, 0, 1, 0, 1, 0, 1, 0, 0, 1, 1,
	}
	tex := NewTexture(src, 3, 1, false)
	if len(tex.Levels) != 3 {
		t.Fatalf("levels = %d, want 3", len(tex.Levels))
	}
	l1 := tex.Levels[1]
	if l1.W != 2 || l1.H != 1 {
		t.Fatalf("level1 dims = %dx%d, want 2x1", l1.W, l1.H)
	}
	want := [][4]float32{{0.5, 0.5, 0, 1}, {0, 0, 1, 1}}
	for i, wv := range want {
		p := i * 4
		for c := 0; c < 4; c++ {
			if !approxEqual(l1.Data[p+c], wv[c], 1e-7) {
				t.Fatalf("level1 texel %d channel %d = %v, want %v", i, c, l1.Data[p+c], wv[c])
			}
		}
	}

	// 5x3 pyramid: check every level's dimensions and verify the final level
	// against an independent float64 evaluation of the same weighted box
	// filter (unequal border weights mean the result is not a plain mean).
	rng := rand.New(rand.NewSource(42))
	w, h := 5, 3
	img := make([]float32, w*h*4)
	for i := 0; i < w*h; i++ {
		for c := 0; c < 4; c++ {
			img[i*4+c] = rng.Float32()
		}
	}
	tex = NewTexture(img, w, h, false)
	wantDims := [][2]int{{5, 3}, {3, 2}, {2, 1}, {1, 1}}
	if len(tex.Levels) != len(wantDims) {
		t.Fatalf("levels = %d, want %d", len(tex.Levels), len(wantDims))
	}
	for l, d := range wantDims {
		if tex.Levels[l].W != d[0] || tex.Levels[l].H != d[1] {
			t.Fatalf("level %d dims = %dx%d, want %dx%d", l,
				tex.Levels[l].W, tex.Levels[l].H, d[0], d[1])
		}
	}
	last := tex.Levels[len(tex.Levels)-1]

	// Independent float64 walk of the documented 2x2 weighted box rule.
	cur := make([][4]float64, w*h)
	for i := range cur {
		for c := 0; c < 4; c++ {
			cur[i][c] = float64(img[i*4+c])
		}
	}
	cw, ch := w, h
	for cw > 1 || ch > 1 {
		nw, nh := (cw+1)/2, (ch+1)/2
		nxt := make([][4]float64, nw*nh)
		for dy := 0; dy < nh; dy++ {
			for dx := 0; dx < nw; dx++ {
				x0, x1 := dx*2, min(dx*2+2, cw)
				y0, y1 := dy*2, min(dy*2+2, ch)
				var sum [4]float64
				for sy := y0; sy < y1; sy++ {
					for sx := x0; sx < x1; sx++ {
						for k := 0; k < 4; k++ {
							sum[k] += cur[sy*cw+sx][k]
						}
					}
				}
				cnt := float64((x1 - x0) * (y1 - y0))
				for k := 0; k < 4; k++ {
					nxt[dy*nw+dx][k] = sum[k] / cnt
				}
			}
		}
		cur, cw, ch = nxt, nw, nh
	}
	for c := 0; c < 4; c++ {
		if d := math.Abs(float64(last.Data[c]) - cur[0][c]); d > 1e-6 {
			t.Fatalf("final level channel %d = %v, want %v (err %e)", c, last.Data[c], cur[0][c], d)
		}
	}
}

func TestSRGBDecodedBeforeFiltering(t *testing.T) {
	// Two sRGB texels 0.0 and 1.0: linear mean must be 0.5, not the sRGB
	// encoding of 0.5 (~0.735), proving interpolation happens in linear.
	img := []float32{
		0, 0, 0, 1, 1, 1, 1, 1,
	}
	tex := NewTexture(img, 2, 1, true)
	l1 := tex.Levels[1]
	for c := 0; c < 3; c++ {
		if !approxEqual(l1.Data[c], 0.5, 1e-7) {
			t.Fatalf("sRGB level1 channel %d = %v, want linear 0.5", c, l1.Data[c])
		}
	}

	// Mid-gray sRGB 0.5 decodes to the canonical linear value.
	lin := DecodeSRGB(0.5)
	wantLin := float32(math.Pow((0.5+0.055)/1.055, 2.4))
	if !approxEqual(lin, wantLin, 1e-7) {
		t.Fatalf("DecodeSRGB(0.5) = %v, want %v", lin, wantLin)
	}
	// Round trip.
	if !approxEqual(EncodeSRGB(DecodeSRGB(0.37)), 0.37, 1e-6) {
		t.Fatalf("sRGB round trip failed")
	}
	// Linear segment near zero.
	if !approxEqual(DecodeSRGB(0.0), 0, 1e-7) {
		t.Fatal("DecodeSRGB(0) != 0")
	}
}

func TestMipMatchesFloat64Reference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, dims := range [][2]int{{5, 3}, {7, 7}, {6, 4}, {13, 9}, {1, 5}, {32, 8}} {
		w, h := dims[0], dims[1]
		img := make([]float32, w*h*4)
		for i := range img {
			img[i] = rng.Float32()
		}

		fast := NewTexture(img, w, h, true)
		ref := NewReferenceTexture(img, w, h, true)
		if len(fast.Levels) != len(ref.levels) {
			t.Fatalf("(%dx%d) level count %d != reference %d", w, h,
				len(fast.Levels), len(ref.levels))
		}
		var maxErr, sumSq float64
		n := 0
		for l := range fast.Levels {
			fl, rl := fast.Levels[l], ref.levels[l]
			if fl.W != ref.dims[l][0] || fl.H != ref.dims[l][1] {
				t.Fatalf("(%dx%d) level %d dimension mismatch", w, h, l)
			}
			for i, v := range fl.Data {
				d := math.Abs(float64(v) - rl[i/4][i%4])
				if d > maxErr {
					maxErr = d
				}
				sumSq += d * d
				n++
			}
		}
		rms := math.Sqrt(sumSq / float64(n))
		t.Logf("sRGB mip %dx%d: maxErr=%.3e rms=%.3e", w, h, maxErr, rms)
		if maxErr > 2e-5 {
			t.Fatalf("(%dx%d) mip max error %e exceeds tolerance", w, h, maxErr)
		}
		if rms > 5e-6 {
			t.Fatalf("(%dx%d) mip rms error %e exceeds tolerance", w, h, rms)
		}
	}
}
