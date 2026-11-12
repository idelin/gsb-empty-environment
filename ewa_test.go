package taskpkg

import (
	"math"
	"testing"
)

func solid(w, h int, c ColorRGBA) []ColorRGBA {
	pix := make([]ColorRGBA, w*h)
	for i := range pix {
		pix[i] = c
	}
	return pix
}

func checker(w, h int) []ColorRGBA {
	black := ColorRGBA{}
	white := ColorRGBA{R: 1, G: 1, B: 1, A: 1}
	pix := make([]ColorRGBA, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x+y)&1 == 0 {
				pix[y*w+x] = white
			} else {
				pix[y*w+x] = black
			}
		}
	}
	return pix
}

func approxColor(got, want ColorRGBA, eps float64) bool {
	return math.Abs(got.R-want.R) <= eps &&
		math.Abs(got.G-want.G) <= eps &&
		math.Abs(got.B-want.B) <= eps &&
		math.Abs(got.A-want.A) <= eps
}

// referenceSample is a dense supersampling ground truth. It scans the unit
// disk in screen space, maps each sample into texture space with the same
// Jacobian as Sample, reads the image via bilinear interpolation (the box
// filter of the piecewise-constant texel grid), and weights it with the EWA
// radial profile; gaussian toggles between the weighted profile and a uniform
// disk average. Reference footprints stay on level zero so mip prefiltering
// does not enter the comparison.
func referenceSample(t *Texture, p SampleParams, steps int, gaussian bool) ColorRGBA {
	var r, g, b, a, weightSum float64
	inv := 1.0 / float64(steps)
	for iy := 0; iy < steps; iy++ {
		sy := (float64(iy)+0.5)*inv*2.0 - 1.0
		for ix := 0; ix < steps; ix++ {
			sx := (float64(ix)+0.5)*inv*2.0 - 1.0
			r2 := sx*sx + sy*sy
			if r2 > 1.0 {
				continue
			}
			weight := 1.0
			if gaussian {
				weight = EllipseWeight(r2)
			}
			u := p.U + p.DuDx*sx + p.DuDy*sy
			v := p.V + p.DvDx*sx + p.DvDy*sy
			c := t.bilinear(0, u*float64(t.width), v*float64(t.height))
			r += c.R * weight
			g += c.G * weight
			b += c.B * weight
			a += c.A * weight
			weightSum += weight
		}
	}
	invWeight := 1.0 / weightSum
	return ColorRGBA{R: r * invWeight, G: g * invWeight, B: b * invWeight, A: a * invWeight}
}

func TestEllipseWeightProfile(t *testing.T) {
	if got := EllipseWeight(1.0); math.Abs(got) > 1e-15 {
		t.Fatalf("boundary weight = %v, want 0", got)
	}
	if got := EllipseWeight(1.000001); got != 0 {
		t.Fatalf("outside weight = %v, want 0", got)
	}
	if got := EllipseWeight(0); math.Abs(got-(1-math.Exp(-2))) > 1e-15 {
		t.Fatalf("center weight = %v, want %v", got, 1-math.Exp(-2))
	}
	if got := EllipseWeight(0.5); math.Abs(got-(math.Exp(-1)-math.Exp(-2))) > 1e-15 {
		t.Fatalf("r^2=0.5 weight = %v", got)
	}
}

func TestConstantTextureNormalization(t *testing.T) {
	want := ColorRGBA{R: 0.3, G: 0.6, B: 0.1, A: 0.7}
	tex := NewTexture(16, 16, solid(16, 16, want), AddressWrap, AddressWrap)

	params := []SampleParams{
		{U: 0.5, V: 0.5, DuDx: 0.02, DvDy: 0.03},
		{U: 0.13, V: 0.77, DuDx: 0.11, DuDy: -0.04, DvDx: 0.03, DvDy: 0.09, MaxAnisotropy: 4},
		{U: 0.01, V: 0.99, DuDx: 0.002, DvDy: 0.002},
		{U: 0.5, V: 0.5, DuDx: 0.4, DuDy: 0.2, DvDx: -0.2, DvDy: 0.4, MaxAnisotropy: 16},
	}
	for i, p := range params {
		got := Sample(tex, p)
		if !approxColor(got, want, 1e-12) {
			t.Fatalf("case %d: normalized weight failed, got %+v want %+v", i, got, want)
		}
	}
}

func TestWeightSumMatchesIntegral(t *testing.T) {
	// A large circular footprint is well resolved by the texel grid, so the
	// discrete weight sum approaches the analytic integral
	// integral_0^1 (exp(-2 r^2)-exp(-2)) 2 r dr = 0.5 - 1.5 e^-2 ~= 0.2965
	// per unit area.
	// Wrap addressing keeps every tap valid even where the ellipse crosses
	// the domain edge, so the discrete sum matches the untruncated integral.
	tex := NewTexture(512, 512, solid(512, 512, ColorRGBA{R: 1, G: 1, B: 1, A: 1}), AddressWrap, AddressWrap)
	// Minor axis spans 16 base texels so level zero covers a wide,
	// well-sampled neighborhood whose discrete sum approaches the integral.
	p := SampleParams{U: 0.25, V: 0.25, DuDx: 16.0 / 512.0, DvDy: 16.0 / 512.0}

	j00 := p.DuDx * float64(tex.width)
	j11 := p.DvDy * float64(tex.height)
	level := 0
	scale := math.Ldexp(1.0, -level)

	gram00, gram01, gram11 := eigenGram(j00*scale, 0, 0, j11*scale)
	g00, g01, g11 := clampGram(gram00, gram01, gram11, DefaultMaxAnisotropy)
	det := g00*g11 - g01*g01
	invDet := 1.0 / det
	q00, q01, q11 := g11*invDet, -g01*invDet, g00*invDet

	cu, cv := p.U*float64(tex.width)*scale, p.V*float64(tex.height)*scale
	radiusU := math.Sqrt(g00)
	radiusV := math.Sqrt(g11)
	var sum float64
	for y := int(math.Floor(cv - radiusV)); y <= int(math.Floor(cv+radiusV)); y++ {
		for x := int(math.Floor(cu - radiusU)); x <= int(math.Floor(cu+radiusU)); x++ {
			dx, dy := float64(x)-cu, float64(y)-cv
			sum += EllipseWeight(q00*dx*dx + 2*q01*dx*dy + q11*dy*dy)
		}
	}

	// Divide the discrete texel sum by the ellipse area (pi * j^2 for a
	// circular footprint) to recover the profile integral per unit area.
	ellipseArea := math.Pi * j00 * j11
	perUnitArea := sum / ellipseArea
	want := 0.5 - 1.5*math.Exp(-2)
	if math.Abs(perUnitArea-want) > 0.02 {
		t.Fatalf("per-unit-area integral = %.4f, analytic %.4f (raw sum %.4f)", perUnitArea, want, sum)
	}
}

func TestCheckerSeamWrapInvariance(t *testing.T) {
	tex := NewTexture(32, 32, checker(32, 32), AddressWrap, AddressWrap)

	// Footprint straddling the U and V periodic borders.
	p := SampleParams{
		U: 0.02, V: 0.98,
		DuDx: 0.06, DuDy: 0.02,
		DvDx: -0.02, DvDy: 0.06,
		MaxAnisotropy: 8,
	}
	got := Sample(tex, p)

	// Reference supersampling on the same wrapped image.
	want := referenceSample(tex, p, 96, true)
	if !approxColor(got, want, 0.02) {
		t.Fatalf("seam filtering got %+v, reference %+v", got, want)
	}

	// Periodicity: shifting the center by a whole number of texture repeats
	// must produce exactly the same filtered color.
	shifted := p
	shifted.U += 3.0
	shifted.V -= 2.0
	if got2 := Sample(tex, shifted); !approxColor(got2, got, 1e-12) {
		t.Fatalf("wrap periodicity broken: %+v vs %+v", got2, got)
	}
}
