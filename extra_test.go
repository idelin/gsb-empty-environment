package texsample

import (
	"math"
	"math/rand"
	"testing"
)

func TestTrilinearOnOddSizedChain(t *testing.T) {
	// 5x3 base: levels are 5x3, 3x2, 2x1, 1x1. Every level carries a distinct
	// constant color so a fractional LOD produces an exact, hand-checkable
	// blend even though no level dimension is a power of two.
	var levels []Level
	w, h := 5, 3
	for l := 0; ; l++ {
		lw, lh := MipDimensions(w, h, l)
		data := make([]float32, lw*lh*4)
		c := float32(l)
		for i := 0; i < lw*lh; i++ {
			data[i*4] = c
			data[i*4+1] = c
			data[i*4+2] = c
			data[i*4+3] = 1
		}
		levels = append(levels, Level{Data: data, W: lw, H: lh})
		if lw == 1 && lh == 1 {
			break
		}
	}
	tex := NewLinearTexture(levels)

	// LOD 1.25: rho = 2^1.25 texels per pixel. Level 1 (color 1) blended 75%
	// with level 2 (color 2) gives 1.25.
	rho := float32(math.Pow(2, 1.25))
	dudx := rho / float32(w)
	s := NewSampler(FilterTrilinear, WrapClamp, WrapClamp)
	got := s.SampleGrad(tex, 0.3, 0.4, dudx, 0, 0, 0)[0]
	if !approxEqual(got, 1.25, 2e-6) {
		t.Fatalf("odd-chain trilinear = %v, want 1.25", got)
	}

	// Reference must agree on the same NPOT pyramid and fractional LOD.
	img := make([]float32, w*h*4)
	rng := rand.New(rand.NewSource(99))
	for i := range img {
		img[i] = rng.Float32()
	}
	fast := NewTexture(img, w, h, false)
	ref := NewReferenceTexture(img, w, h, false)
	for trial := 0; trial < 25; trial++ {
		u := rng.Float32()*2 - 0.5
		v := rng.Float32()*2 - 0.5
		span := rng.Float64()*6 + 0.25
		dx := float32(span / float64(w))
		dy := float32(span / float64(h))
		got := s.SampleGrad(fast, u, v, dx, 0, 0, dy)
		want := ref.SampleGrad(FilterTrilinear, WrapClamp, WrapClamp,
			float64(u), float64(v), float64(dx), 0, 0, float64(dy),
			0, 0, 1e300)
		for c := 0; c < 4; c++ {
			if d := abs64(float64(got[c]) - want[c]); d > 2e-5 {
				t.Fatalf("trial %d channel %d err %e", trial, c, d)
			}
		}
	}
}

func TestMirrorBilinearBoundary(t *testing.T) {
	// 4x1 texture; at u just below 0 the mirror footprint wraps to texel 1,
	// and at u just above 0 it uses texel 0. The blend must be symmetric.
	img := []float32{
		0, 0, 0, 1, 1, 1, 1, 1, 2, 2, 2, 1, 3, 3, 3, 1,
	}
	tex := NewTexture(img, 4, 1, false)
	s := NewSampler(FilterBilinear, WrapMirror, WrapMirror)

	// u = 0 is a texel center under the half-texel inset -> texel 0.
	if got := s.Sample(tex, 0, 0.5)[0]; !approxEqual(got, 0, 1e-7) {
		t.Fatalf("mirror bilinear u=0 = %v, want 0", got)
	}
	// One full period away must give the same value.
	a := s.Sample(tex, 0.2, 0.5)[0]
	b := s.Sample(tex, -0.2, 0.5)[0]
	if !approxEqual(a, b, 1e-7) {
		t.Fatalf("mirror periodicity broken: %v != %v", a, b)
	}
}

func TestZeroDerivativesUseLevelZero(t *testing.T) {
	tex := checkerTexture(8, 8)
	s := NewSampler(FilterTrilinear, WrapRepeat, WrapRepeat)
	grad := s.SampleGrad(tex, 0.5, 0.5, 0, 0, 0, 0)
	plain := s.Sample(tex, 0.5, 0.5)
	for c := 0; c < 4; c++ {
		if !approxEqual(grad[c], plain[c], 1e-7) {
			t.Fatalf("zero derivative channel %d: %v != %v", c, grad[c], plain[c])
		}
	}
}

func TestAlphaPassesThroughSRGB(t *testing.T) {
	img := []float32{0.5, 0.5, 0.5, 0.37, 0.5, 0.5, 0.5, 0.89}
	tex := NewTexture(img, 2, 1, true)
	l1 := tex.Levels[1]
	if !approxEqual(l1.Data[3], (0.37+0.89)/2, 1e-7) {
		t.Fatalf("alpha averaged as linear, got %v", l1.Data[3])
	}
	// Color channels were decoded, so they are not the raw sRGB average.
	srgbMean := float32(0.5)
	if approxEqual(l1.Data[0], srgbMean, 1e-3) {
		t.Fatal("color channel appears to have been averaged in sRGB space")
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
