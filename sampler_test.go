package taskpkg

import (
	"math"
	"testing"
)

// lcg is a deterministic, dependency-free uniform random source for tests.
type lcg struct {
	state uint64
}

func newLCG(seed uint64) *lcg { return &lcg{state: seed} }

func (r *lcg) float64() float64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return float64(r.state>>11) / float64(uint64(1)<<53)
}

func TestConstantEnvironmentPDF(t *testing.T) {
	n := 8
	luminance := 3.5
	cube, err := NewCubeMap(n, fillFaces(float64(n), luminance))
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-12)
	if sampler.Uniform() {
		t.Fatal("constant non-black environment must not use the fallback")
	}
	nearlyEqual(t, sampler.Integral(), luminance*4*math.Pi, 1e-10, "constant luminance integral")

	rng := newLCG(12345)
	for sample := 0; sample < 2000; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		nearlyEqual(t, direction.Length(), 1, 1e-12, "sampled direction length")
		nearlyEqual(t, pdf, 1/(4*math.Pi), 1e-10, "constant-environment PDF")
		nearlyEqual(t, sampler.PDF(direction), 1/(4*math.Pi), 1e-10, "evaluated PDF")
	}
}

func TestPerFaceLuminanceAndRotationRoundTrip(t *testing.T) {
	n := 4
	grids := [FaceCount][]float64{}
	faceLuminance := [FaceCount]float64{1, 2, 3, 4, 5, 6}
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, n*n)
		for index := range grids[face] {
			grids[face][index] = faceLuminance[face]
		}
	}
	cube, err := NewCubeMap(n, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-12)

	totalLuminance := 0.0
	for _, luminance := range faceLuminance {
		totalLuminance += luminance
	}

	// Every face spans the same solid angle, so the face PDF is constant and
	// the discrete probability of selecting a face is L_face / sum(L).
	for face := 0; face < FaceCount; face++ {
		expectedPDF := faceLuminance[face] / (totalLuminance * 4 * math.Pi / FaceCount)
		direction := faces[face].direction(0.2, -0.7)
		nearlyEqual(t, sampler.PDF(direction), expectedPDF, 1e-12, "per-face PDF")

		// Rotation/permutation round trip: canonical (u, v) survives the
		// face frame rotation and inverse mapping.
		for _, u := range []float64{-0.9, -0.31, 0.17, 0.83} {
			for _, v := range []float64{-0.77, -0.05, 0.42, 0.95} {
				mapped := faces[face].direction(u, v)
				owner, gotU, gotV := locateDirection(mapped)
				if owner != face {
					t.Fatalf("face %d round trip owned by %d", face, owner)
				}
				nearlyEqual(t, gotU, u, 1e-12, "round-trip u")
				nearlyEqual(t, gotV, v, 1e-12, "round-trip v")
			}
		}
	}

	// Empirical face selection frequencies follow the luminance-weighted area
	// distribution because all six faces have equal total solid angle.
	rng := newLCG(9876)
	hits := [FaceCount]int{}
	const samples = 60000
	for sample := 0; sample < samples; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		if !(pdf > 0) {
			t.Fatal("zero PDF for a lit face")
		}
		face, _, _ := locateDirection(direction)
		hits[face]++
	}
	for face := 0; face < FaceCount; face++ {
		got := float64(hits[face]) / samples
		want := faceLuminance[face] / totalLuminance
		if math.Abs(got-want) > 0.01 {
			t.Fatalf("face %d empirical fraction %.4f, want %.4f", face, got, want)
		}
	}
}
