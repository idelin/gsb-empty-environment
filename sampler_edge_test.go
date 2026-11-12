package taskpkg

import (
	"math"
	"testing"
)

// n=1 pixels span u, v in [-1, 1] and cross both zero axes; this is the
// regression case for signed CDF primitives collapsing to a boundary.
func TestSinglePixelPerFaceAreaDistribution(t *testing.T) {
	grids := [FaceCount][]float64{}
	for face := 0; face < FaceCount; face++ {
		grids[face] = []float64{0}
	}
	grids[FacePosZ][0] = 1
	cube, err := NewCubeMap(1, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-11)

	const samples = 50000
	const strips = 10
	uCounts := make([]int, strips)
	vCounts := make([]int, strips)
	rng := newLCG(555)
	for sample := 0; sample < samples; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		face, u, v := locateDirection(direction)
		if face != FacePosZ {
			t.Fatalf("n=1 sample landed on face %d, want +Z", face)
		}
		expectedPDF := 1 / (4 * math.Pi / FaceCount)
		nearlyEqual(t, pdf, expectedPDF, 1e-12, "n=1 single-face PDF")

		uStrip := int((u + 1) / 2 * strips)
		vStrip := int((v + 1) / 2 * strips)
		if uStrip == strips {
			uStrip--
		}
		if vStrip == strips {
			vStrip--
		}
		uCounts[uStrip]++
		vCounts[vStrip]++
	}

	// Solid-angle mass of each u strip of the full +Z face.
	for strip := 0; strip < strips; strip++ {
		u0 := -1 + float64(strip)/strips*2
		u1 := -1 + float64(strip+1)/strips*2
		want := solidAngleRect(u0, u1, -1, 1) / (4 * math.Pi / FaceCount)
		got := float64(uCounts[strip]) / samples
		if math.Abs(got-want) > 0.02 {
			t.Fatalf("u strip %d fraction %.4f, want %.4f", strip, got, want)
		}

		v0 := -1 + float64(strip)/strips*2
		v1 := -1 + float64(strip+1)/strips*2
		wantV := solidAngleRect(-1, 1, v0, v1) / (4 * math.Pi / FaceCount)
		gotV := float64(vCounts[strip]) / samples
		if math.Abs(gotV-wantV) > 0.02 {
			t.Fatalf("v strip %d fraction %.4f, want %.4f", strip, gotV, wantV)
		}
	}
}

func TestStratifiedDistributionProportionalSelection(t *testing.T) {
	weights := []float64{0, 1, 3, 0, 6}
	dist := newStratifiedDistribution(weights)
	nearlyEqual(t, dist.total(), 10, 1e-15, "tree total")

	rng := newLCG(909)
	hits := make([]int, len(weights))
	const samples = 40000
	for sample := 0; sample < samples; sample++ {
		index, probability := dist.sample(rng.float64())
		if weights[index] == 0 {
			t.Fatalf("zero-weight leaf %d selected", index)
		}
		nearlyEqual(t, probability, weights[index]/10, 1e-15, "leaf probability")
		hits[index]++
	}
	for index, weight := range weights {
		if weight == 0 {
			if hits[index] != 0 {
				t.Fatalf("zero-weight leaf %d hit %d times", index, hits[index])
			}
			continue
		}
		got := float64(hits[index]) / samples
		want := weight / 10
		if math.Abs(got-want) > 0.015 {
			t.Fatalf("leaf %d empirical fraction %.4f, want %.4f", index, got, want)
		}
	}

	empty := newStratifiedDistribution(nil)
	if index, probability := empty.sample(0.5); probability != 0 || index != 0 {
		t.Fatalf("empty distribution returned index=%d probability=%g", index, probability)
	}
}

func TestSamplerRejectsNilCubeMap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil cube map")
		}
	}()
	NewSampler(nil, 1e-9)
}

func TestInvertMonotoneHandlesDecreasingAndSignedCDF(t *testing.T) {
	eps := 1e-9

	// Increasing primitive on a positive-u pixel.
	increasing := func(x float64) float64 { return rectPrimitive(0.5, x) }
	for _, trueX := range []float64{-0.8, -0.2, 0.3, 0.9} {
		got := invertMonotone(increasing, -1, 1, increasing(trueX), eps)
		if math.Abs(got-trueX) > eps {
			t.Fatalf("increasing inversion error %.3g", math.Abs(got-trueX))
		}
	}

	// Decreasing conditional CDF on a negative-u pixel crossing zero.
	decreasing := func(v float64) float64 {
		return rectPrimitive(v, -0.6) - rectPrimitive(-0.4, -0.6)
	}
	if decreasing(1) >= decreasing(-0.4) {
		t.Fatal("test CDF is not decreasing as expected")
	}
	for _, trueV := range []float64{-0.35, 0.0, 0.25, 0.9} {
		got := invertMonotone(decreasing, -0.4, 1, decreasing(trueV), eps)
		if math.Abs(got-trueV) > eps {
			t.Fatalf("decreasing inversion error %.3g for trueV %.2f", math.Abs(got-trueV), trueV)
		}
	}
}
