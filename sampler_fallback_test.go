package taskpkg

import (
	"math"
	"testing"
)

func TestEpsBoundsInversionError(t *testing.T) {
	eps := 1e-6
	u0, u1, v0, v1 := -0.75, -0.25, 0.1, 0.6
	marginal := func(u float64) float64 {
		return rectPrimitive(v1, u) - rectPrimitive(v0, u) -
			rectPrimitive(v1, u0) + rectPrimitive(v0, u0)
	}
	conditional := func(v float64) float64 { return rectPrimitive(v, 0.4) }

	for _, trueU := range []float64{-0.7, -0.5, -0.3} {
		target := marginal(trueU)
		got := invertMonotone(marginal, u0, u1, target, eps)
		if math.Abs(got-trueU) > eps {
			t.Fatalf("marginal inversion error %.3g exceeds eps %.3g", math.Abs(got-trueU), eps)
		}
	}
	for _, trueV := range []float64{0.15, 0.35, 0.55} {
		target := conditional(trueV)
		got := invertMonotone(conditional, v0, v1, target, eps)
		if math.Abs(got-trueV) > eps {
			t.Fatalf("conditional inversion error %.3g exceeds eps %.3g", math.Abs(got-trueV), eps)
		}
	}

	// Smaller caller tolerance produces no weaker precision.
	tight := invertMonotone(marginal, u0, u1, marginal(-0.5), 1e-10)
	if math.Abs(tight+0.5) > 1e-9 {
		t.Fatalf("tight inversion error %.3g exceeds 1e-9", math.Abs(tight+0.5))
	}
}

func TestAllBlackFallsBackToUniformSphere(t *testing.T) {
	n := 5
	cube, err := NewCubeMap(n, fillFaces(float64(n), 0))
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-9)
	if !sampler.Uniform() {
		t.Fatal("all-black map must enable the uniform fallback")
	}
	if sampler.Integral() != 0 {
		t.Fatalf("all-black integral = %g, want 0", sampler.Integral())
	}

	rng := newLCG(31337)
	const samples = 60000
	meanZ := 0.0
	zBins := 8
	counts := make([]int, zBins)
	for sample := 0; sample < samples; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		nearlyEqual(t, direction.Length(), 1, 1e-12, "uniform direction length")
		nearlyEqual(t, pdf, 1/(4*math.Pi), 1e-15, "uniform PDF")
		nearlyEqual(t, sampler.PDF(direction), 1/(4*math.Pi), 1e-15, "evaluated uniform PDF")
		meanZ += direction.Z
		bin := int((direction.Z + 1) / 2 * float64(zBins))
		if bin == zBins {
			bin--
		}
		counts[bin]++
	}
	nearlyEqual(t, meanZ/samples, 0, 0.02, "uniform mean z")
	for bin, count := range counts {
		got := float64(count) / samples
		if math.Abs(got-1/float64(zBins)) > 0.02 {
			t.Fatalf("z bin %d empirical fraction %.4f, want %.4f", bin, got, 1/float64(zBins))
		}
	}
}

func TestZeroLuminancePixelWeightsAreZero(t *testing.T) {
	n := 3
	grids := [FaceCount][]float64{}
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, n*n)
		for index := range grids[face] {
			grids[face][index] = 4
		}
	}
	grids[FaceNegY][n] = 0
	cube, err := NewCubeMap(n, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-10)
	if weight := sampler.PixelWeight(FaceNegY, 0, 1); weight != 0 {
		t.Fatalf("black pixel weight = %g, want 0", weight)
	}
	if weight := sampler.PixelWeight(FacePosX, 0, 0); weight <= 0 {
		t.Fatalf("lit pixel weight = %g, want positive", weight)
	}
}
