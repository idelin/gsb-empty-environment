package texsample

import (
	"math"
	"math/rand"
	"testing"
)

// checker builds a 4x4 texture whose texel value is its flat index, so each
// texel is uniquely identifiable.
func checkerTexture(w, h int) *Texture {
	img := make([]float32, w*h*4)
	for i := 0; i < w*h; i++ {
		v := float32(i) / float32(w*h-1)
		img[i*4] = v
		img[i*4+1] = v
		img[i*4+2] = v
		img[i*4+3] = 1
	}
	return NewTexture(img, w, h, false)
}

func TestWrapIndexModes(t *testing.T) {
	// n=4 repeat.
	wantRepeat := map[int]int{-5: 3, -4: 0, -1: 3, 0: 0, 3: 3, 4: 0, 5: 1, 8: 0}
	for in, want := range wantRepeat {
		if got := wrapIndex(in, 4, WrapRepeat); got != want {
			t.Fatalf("repeat wrapIndex(%d) = %d, want %d", in, got, want)
		}
	}
	// n=4 mirror over period 2n with the reflected segment on the negative
	// side. Continuous index mapping, including negative period boundaries:
	//   indices -8..-1 -> 0 1 2 3 3 2 1 0
	//   indices  0..7  -> 0 1 2 3 3 2 1 0
	wantMirror := map[int]int{
		-8: 0, -7: 1, -6: 2, -5: 3, -4: 3, -3: 2, -2: 1, -1: 0,
		0: 0, 1: 1, 2: 2, 3: 3, 4: 3, 5: 2, 6: 1, 7: 0,
		8: 0, 9: 1, 10: 2, 11: 3,
	}
	for in, want := range wantMirror {
		if got := wrapIndex(in, 4, WrapMirror); got != want {
			t.Fatalf("mirror wrapIndex(%d) = %d, want %d", in, got, want)
		}
	}
	// Clamp.
	for _, in := range []int{-9, -1} {
		if got := wrapIndex(in, 4, WrapClamp); got != 0 {
			t.Fatalf("clamp wrapIndex(%d) = %d, want 0", in, got)
		}
	}
	for _, in := range []int{4, 99} {
		if got := wrapIndex(in, 4, WrapClamp); got != 3 {
			t.Fatalf("clamp wrapIndex(%d) = %d, want 3", in, got)
		}
	}
}

func TestNearestTexelCenters(t *testing.T) {
	const n = 4
	tex := checkerTexture(n, n)
	s := NewSampler(FilterNearest, WrapClamp, WrapClamp)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			u := (float32(x) + 0.5) / n
			v := (float32(y) + 0.5) / n
			got := s.Sample(tex, u, v)
			want := tex.Levels[0].Data[(y*n+x)*4]
			if !approxEqual(got[0], want, 1e-7) {
				t.Fatalf("nearest at (%d,%d) = %v, want %v", x, y, got[0], want)
			}
		}
	}
}

func TestBilinearHalfwayAndEdges(t *testing.T) {
	// 2x1 texture with values 0.2 and 0.8.
	img := []float32{0.2, 0.2, 0.2, 1, 0.8, 0.8, 0.8, 1}
	tex := NewTexture(img, 2, 1, false)
	s := NewSampler(FilterBilinear, WrapClamp, WrapClamp)

	// Edges land on texel centers due to the half-texel inset.
	if got := s.Sample(tex, 0, 0.5)[0]; !approxEqual(got, 0.2, 1e-7) {
		t.Fatalf("bilinear u=0 = %v, want 0.2", got)
	}
	if got := s.Sample(tex, 1, 0.5)[0]; !approxEqual(got, 0.8, 1e-7) {
		t.Fatalf("bilinear u=1 = %v, want 0.8", got)
	}
	// u=0.5 is exactly halfway between the two texel centers.
	if got := s.Sample(tex, 0.5, 0.5)[0]; !approxEqual(got, 0.5, 1e-7) {
		t.Fatalf("bilinear u=0.5 = %v, want 0.5", got)
	}
}

func TestLODSelection(t *testing.T) {
	// 8x8 texture where every mip level gets a distinct constant color via a
	// hand-built linear texture.
	var levels []Level
	for l := 0; l < 4; l++ {
		dw, dh := MipDimensions(8, 8, l)
		data := make([]float32, dw*dh*4)
		c := float32(l)
		for i := 0; i < dw*dh; i++ {
			data[i*4] = c
			data[i*4+1] = c
			data[i*4+2] = c
			data[i*4+3] = 1
		}
		levels = append(levels, Level{Data: data, W: dw, H: dh})
	}
	tex := NewLinearTexture(levels)

	// Derivative of one full texel per pixel => rho = 1 => LOD 0.
	oneTexel := float32(1.0 / 8.0)
	cases := []struct {
		name string
		f    Filter
		rho  float32 // texel span per pixel along x
		want float32 // expected level color (or blend)
		eps  float32
	}{
		{"nearest LOD2", FilterNearest, 4, 2, 1e-6},
		{"bilinear LOD2", FilterBilinear, 4, 2, 1e-6},
		{"nearest LOD3", FilterNearest, 8, 3, 1e-6},
		{"trilinear halfway 1-2", FilterTrilinear, float32(2 * math.Sqrt2), 1.5, 2e-6},
		{"trilinear LOD clamp", FilterTrilinear, 64, 3, 1e-6},
		{"magnification LOD0", FilterTrilinear, 0, 0, 1e-6},
	}
	for _, tc := range cases {
		s := NewSampler(tc.f, WrapClamp, WrapClamp)
		got := s.SampleGrad(tex, 0.5, 0.5, tc.rho*oneTexel, 0, 0, 0)[0]
		if !approxEqual(got, tc.want, tc.eps) {
			t.Fatalf("%s: got level color %v, want %v (rho=%v)", tc.name, got, tc.want, tc.rho)
		}
	}

	// Nearest rounding: rho=3 gives LOD log2(3)=1.585 -> rounds to level 2.
	s := NewSampler(FilterNearest, WrapClamp, WrapClamp)
	if got := s.SampleGrad(tex, 0.5, 0.5, 3*oneTexel, 0, 0, 0)[0]; !approxEqual(got, 2, 1e-6) {
		t.Fatalf("nearest LOD rounding got %v, want 2", got)
	}
}

func TestSampleMatchesFloat64Reference(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	filters := []Filter{FilterNearest, FilterBilinear, FilterTrilinear}
	wraps := []WrapMode{WrapRepeat, WrapMirror, WrapClamp}

	var globalMax float64
	for _, dims := range [][2]int{{5, 3}, {8, 8}, {7, 5}, {13, 9}, {1, 6}} {
		w, h := dims[0], dims[1]
		img := make([]float32, w*h*4)
		for i := range img {
			img[i] = rng.Float32()
		}
		fast := NewTexture(img, w, h, true)
		ref := NewReferenceTexture(img, w, h, true)

		for _, f := range filters {
			for _, ws := range wraps {
				for trial := 0; trial < 40; trial++ {
					// Coordinates deliberately outside [0,1] to exercise wraps,
					// plus derivatives spanning magnification and minification.
					u := rng.Float32()*2.4 - 0.7
					v := rng.Float32()*2.4 - 0.7
					span := math.Pow(2, rng.Float64()*7-1) // 0.5 .. 64 texels/pixel
					dudx := float32(span / float64(w))
					dvdy := float32(span / float64(h))

					s := NewSampler(f, ws, ws)
					got := s.SampleGrad(fast, u, v, dudx, 0, 0, dvdy)
					want := ref.SampleGrad(f, ws, ws,
						float64(u), float64(v),
						float64(dudx), 0, 0, float64(dvdy),
						0, 0, math.MaxFloat64)
					for c := 0; c < 4; c++ {
						d := math.Abs(float64(got[c]) - want[c])
						if d > globalMax {
							globalMax = d
						}
						if d > 2e-5 {
							t.Fatalf("(%dx%d) f=%v wrap=%v trial=%d channel=%d: "+
								"got %v want %v err %e", w, h, f, ws, trial, c, got[c], want[c], d)
						}
					}
				}
			}
		}
	}
	t.Logf("sampler max absolute error vs float64 reference: %.3e", globalMax)
}

func TestLODBiasAndClamp(t *testing.T) {
	// Reuse the distinct-level texture from TestLODSelection.
	var levels []Level
	for l := 0; l < 4; l++ {
		dw, dh := MipDimensions(8, 8, l)
		data := make([]float32, dw*dh*4)
		for i := 0; i < dw*dh; i++ {
			data[i*4] = float32(l)
			data[i*4+1] = float32(l)
			data[i*4+2] = float32(l)
			data[i*4+3] = 1
		}
		levels = append(levels, Level{Data: data, W: dw, H: dh})
	}
	tex := NewLinearTexture(levels)
	oneTexel := float32(1.0 / 8.0)

	// Base LOD 0 (rho=1); bias +2 forces level 2.
	s := NewSampler(FilterNearest, WrapClamp, WrapClamp)
	s.LODBias = 2
	if got := s.SampleGrad(tex, 0.5, 0.5, oneTexel, 0, 0, 0)[0]; !approxEqual(got, 2, 1e-6) {
		t.Fatalf("LOD bias got %v, want 2", got)
	}

	// MaxLOD 1 clamps even though rho=8 would select level 3.
	s = NewSampler(FilterNearest, WrapClamp, WrapClamp)
	s.MaxLOD = 1
	if got := s.SampleGrad(tex, 0.5, 0.5, 8*oneTexel, 0, 0, 0)[0]; !approxEqual(got, 1, 1e-6) {
		t.Fatalf("MaxLOD clamp got %v, want 1", got)
	}
}
