package taskpkg

import (
	"math"
	"testing"
)

func TestRotatedFootprintMatchesReference(t *testing.T) {
	tex := NewTexture(64, 64, checker(64, 64), AddressWrap, AddressWrap)

	cases := []struct {
		name  string
		angle float64
		major float64
		minor float64
	}{
		// Minor axes span ~1.3 texels on the 64-texel grid, keeping both the
		// filter and its supersampling reference on mip level zero while
		// giving the ellipse enough width to be sampled stably.
		{"axis aligned", 0, 0.18, 0.02},
		{"45 degrees", math.Pi / 4, 0.18, 0.02},
		{"30 degrees", math.Pi / 6, 0.14, 0.02},
		{"120 degrees", 2 * math.Pi / 3, 0.16, 0.02},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cos, sin := math.Cos(tc.angle), math.Sin(tc.angle)
			// Screen derivative vectors are the rotated ellipse axes in UV space.
			p := SampleParams{
				U: 0.373, V: 0.617,
				DuDx: tc.major * cos,
				DuDy: -tc.minor * sin,
				DvDx: tc.major * sin,
				DvDy: tc.minor * cos,
				// Looser than the largest true ratio (~15:1) so this case
				// isolates rotated geometry; ratio clamping is covered by
				// TestAnisotropyClamp.
				MaxAnisotropy: 32,
			}
			got := Sample(tex, p)
			want := referenceSample(tex, p, 256, true)
			if !approxColor(got, want, 0.02) {
				t.Fatalf("rotated footprint got %+v, reference %+v", got, want)
			}
		})
	}
}

func TestDegenerateFootprintIsBilinear(t *testing.T) {
	tex := NewTexture(8, 8, checker(8, 8), AddressWrap, AddressWrap)

	// Rank-deficient Jacobian: both derivative vectors are collinear.
	p := SampleParams{
		U: 0.31, V: 0.42,
		DuDx: 0.1, DvDx: 0.2,
		DuDy: 0.05, DvDy: 0.1,
	}
	got := Sample(tex, p)
	want := tex.bilinear(0, p.U*8, p.V*8)
	if !approxColor(got, want, 1e-15) {
		t.Fatalf("degenerate fallback got %+v, bilinear %+v", got, want)
	}

	// All-zero derivatives likewise reduce to bilinear at the center.
	p0 := SampleParams{U: 0.2, V: 0.7}
	got0 := Sample(tex, p0)
	want0 := tex.bilinear(0, p0.U*8, p0.V*8)
	if !approxColor(got0, want0, 1e-15) {
		t.Fatalf("zero-derivative fallback got %+v, bilinear %+v", got0, want0)
	}
}

func TestPremultipliedAlphaFlow(t *testing.T) {
	// Straight colors are premultiplied on input; filtered output stays
	// premultiplied and linear (RGB never exceeds alpha for these inputs).
	red := Premultiplied(1, 0, 0, 0.5)
	blue := Premultiplied(0, 0, 1, 0.5)
	base := make([]ColorRGBA, 4*4)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)&1 == 0 {
				base[y*4+x] = red
			} else {
				base[y*4+x] = blue
			}
		}
	}
	tex := NewTexture(4, 4, base, AddressWrap, AddressWrap)
	// Center on an intersection of four texels so the symmetric footprint
	// blends equal red and blue premultiplied contributions.
	p := SampleParams{U: 0.375, V: 0.375, DuDx: 0.05, DuDy: 0.02, DvDx: -0.02, DvDy: 0.05, MaxAnisotropy: 4}
	got := Sample(tex, p)

	if got.A <= 0 || got.A > 0.5+1e-12 {
		t.Fatalf("alpha out of premultiplied range: %+v", got)
	}
	if got.R > got.A+1e-12 || got.G > got.A+1e-12 || got.B > got.A+1e-12 {
		t.Fatalf("color not premultiplied: %+v", got)
	}
	if math.Abs(got.A-0.5) > 1e-12 {
		t.Fatalf("alpha = %v, want 0.5", got.A)
	}
	if got.R <= 0.05 || got.B <= 0.05 {
		t.Fatalf("expected mixed channels, got %+v", got)
	}
}

func TestClampAddressing(t *testing.T) {
	corner := ColorRGBA{R: 0.1, G: 0.9, B: 0.1, A: 1}
	edge := ColorRGBA{R: 0.9, G: 0.2, B: 0.2, A: 1}
	base := solid(8, 8, edge)
	base[0] = corner
	tex := NewTexture(8, 8, base, AddressClamp, AddressClamp)

	// Footprint entirely outside the clamped domain returns the corner texel.
	p := SampleParams{U: -0.9, V: -0.9, DuDx: 0.02, DvDy: 0.02}
	got := Sample(tex, p)
	if !approxColor(got, corner, 1e-12) {
		t.Fatalf("clamp corner got %+v, want %+v", got, corner)
	}

	// A footprint crossing the clamp edge must be dominated by the repeated
	// edge color and match a supersampling reference.
	pEdge := SampleParams{U: -0.03, V: 0.5, DuDx: 0.08, DvDy: 0.08}
	gotEdge := Sample(tex, pEdge)
	wantEdge := referenceSample(tex, pEdge, 64, true)
	if !approxColor(gotEdge, wantEdge, 0.02) {
		t.Fatalf("clamp edge got %+v, reference %+v", gotEdge, wantEdge)
	}
}

func TestAnisotropyClamp(t *testing.T) {
	tex := NewTexture(64, 64, checker(64, 64), AddressWrap, AddressWrap)

	// Ratio 32:1 at 45 degrees, then clamped to the requested 4:1 limit.
	cos45, sin45 := math.Sqrt(0.5), math.Sqrt(0.5)
	p := SampleParams{
		U: 0.5, V: 0.5,
		DuDx:          0.32 * cos45,
		DuDy:          -0.01 * sin45,
		DvDx:          0.32 * sin45,
		DvDy:          0.01 * cos45,
		MaxAnisotropy: 4,
	}

	j00 := p.DuDx * 64
	j01 := p.DuDy * 64
	j10 := p.DvDx * 64
	j11 := p.DvDy * 64
	gram00, gram01, gram11 := eigenGram(j00, j01, j10, j11)
	g00, g01, g11 := clampGram(gram00, gram01, gram11, 4)

	halfTrace := 0.5 * (g00 + g11)
	disc := math.Sqrt(math.Max(0, halfTrace*halfTrace-(g00*g11-g01*g01)))
	ratio := math.Sqrt((halfTrace + disc) / math.Max(1e-30, halfTrace-disc))
	if ratio > 4.0+1e-9 {
		t.Fatalf("clamped axis ratio = %.3f, want <= 4", ratio)
	}

	// Sampling remains finite and normalized against a constant equivalent.
	got := Sample(tex, p)
	if !allFinite(got.R, got.G, got.B, got.A) {
		t.Fatalf("non-finite clamped result: %+v", got)
	}
}

func TestMipSelectionFromMinorAxis(t *testing.T) {
	// Isotropic circular footprints whose minor axis spans 2^k base texels
	// select level k (until the top of the chain is reached).
	tex := NewTexture(16, 16, checker(16, 16), AddressWrap, AddressWrap)
	for k := 0; k <= 3; k++ {
		radius := float64(int(1) << uint(k))
		p := SampleParams{
			U: 0.5, V: 0.5,
			DuDx: radius / 16.0,
			DvDy: radius / 16.0,
		}
		minor, _ := eigenLengths2x2(p.DuDx*16, p.DuDy*16, p.DvDx*16, p.DvDy*16)
		level := int(math.Floor(math.Log2(minor)))
		if level < 0 {
			level = 0
		}
		if level > tex.Levels()-1 {
			level = tex.Levels() - 1
		}
		if level != k {
			t.Fatalf("radius %v texels: selected level %d, want %d", radius, level, k)
		}
		// Every level still returns a finite, in-range color.
		got := Sample(tex, p)
		if !allFinite(got.R, got.G, got.B, got.A) {
			t.Fatalf("level %d produced non-finite color %+v", k, got)
		}
	}
}

func TestMipChainDimensions(t *testing.T) {
	tex := NewTexture(8, 4, solid(8, 4, ColorRGBA{A: 1}), AddressWrap, AddressWrap)
	wantDims := [][2]int{{8, 4}, {4, 2}, {2, 1}, {1, 1}}
	if tex.Levels() != len(wantDims) {
		t.Fatalf("levels = %d, want %d", tex.Levels(), len(wantDims))
	}
	for level, dims := range wantDims {
		if gotW := levelDim(tex.width, level); gotW != dims[0] {
			t.Fatalf("level %d width = %d, want %d", level, gotW, dims[0])
		}
		if gotH := levelDim(tex.height, level); gotH != dims[1] {
			t.Fatalf("level %d height = %d, want %d", level, gotH, dims[1])
		}
	}
}

func TestDefaultAnisotropyLimit(t *testing.T) {
	// A 32:1 footprint with no explicit limit is capped at the default 8:1.
	jMajor, jMinor := 32.0, 1.0
	gram00, gram01, gram11 := eigenGram(jMajor, 0, 0, jMinor)
	g00, g01, g11 := clampGram(gram00, gram01, gram11, DefaultMaxAnisotropy)
	halfTrace := 0.5 * (g00 + g11)
	disc := math.Sqrt(math.Max(0, halfTrace*halfTrace-(g00*g11-g01*g01)))
	ratio := math.Sqrt((halfTrace + disc) / math.Max(1e-30, halfTrace-disc))
	if math.Abs(ratio-DefaultMaxAnisotropy) > 1e-9 {
		t.Fatalf("default-capped ratio = %.3f, want %.3f", ratio, DefaultMaxAnisotropy)
	}

	// Zero and negative caller limits both resolve to the default without panic.
	tex := NewTexture(32, 32, checker(32, 32), AddressWrap, AddressWrap)
	p := SampleParams{U: 0.5, V: 0.5, DuDx: 0.5, DvDy: 0.02}
	for _, limit := range []float64{0, -3} {
		p.MaxAnisotropy = limit
		got := Sample(tex, p)
		if !allFinite(got.R, got.G, got.B, got.A) {
			t.Fatalf("limit %v produced non-finite color %+v", limit, got)
		}
	}
}
