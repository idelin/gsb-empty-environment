package taskpkg

import (
	"math"
	"testing"
)

func TestPDFNormalizationByNumericalIntegration(t *testing.T) {
	n := 6
	grids := [FaceCount][]float64{}
	rng := newLCG(20261112)
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, n*n)
		for index := range grids[face] {
			if rng.float64() < 0.3 {
				continue
			}
			grids[face][index] = rng.float64() * 10
		}
	}
	// Guarantee at least one lit pixel.
	grids[FacePosZ][0] = 7

	cube, err := NewCubeMap(n, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-12)

	// Closed-form sphere integral from exact per-pixel solid angles.
	exactIntegral := 0.0
	for face := 0; face < FaceCount; face++ {
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				exactIntegral += cube.Luminance(face, i, j) * cube.SolidAngle(face, i, j)
			}
		}
	}
	if exactIntegral <= 0 {
		t.Fatal("test setup produced an all-black map")
	}
	nearlyEqual(t, sampler.Integral(), exactIntegral, 1e-12, "tree total vs exact integral")

	// Stratified spherical quadrature: integral of the PDF must be one and
	// integral of luminance must match the exact sphere-area integral.
	const thetaBins = 240
	const phiBins = 480
	dTheta := math.Pi / thetaBins
	dPhi := 2 * math.Pi / phiBins
	pdfMass := 0.0
	luminanceMass := 0.0
	for ti := 0; ti < thetaBins; ti++ {
		theta := (float64(ti) + 0.5) * dTheta
		sinTheta := math.Sin(theta)
		for pi := 0; pi < phiBins; pi++ {
			phi := (float64(pi) + 0.5) * dPhi
			direction := Vec3{
				sinTheta * math.Cos(phi),
				sinTheta * math.Sin(phi),
				math.Cos(theta),
			}
			weight := sinTheta * dTheta * dPhi
			pdfMass += sampler.PDF(direction) * weight
			luminanceMass += luminanceAtDirection(cube, direction) * weight
		}
	}
	nearlyEqual(t, pdfMass, 1, 2e-3, "integrated PDF")
	nearlyEqual(t, luminanceMass, exactIntegral, 2e-2*exactIntegral, "integrated luminance")
}

func luminanceAtDirection(cube *CubeMap, direction Vec3) float64 {
	n := cube.Resolution()
	face, u, v := locateDirection(direction)
	i := coordinateIndex(u, n)
	j := coordinateIndex(-v, n)
	return cube.Luminance(face, i, j)
}

func TestLitPixelsNeverZeroProbability(t *testing.T) {
	n := 6
	grids := [FaceCount][]float64{}
	rng := newLCG(424242)
	litLeaves := map[int]bool{}
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, n*n)
		for index := range grids[face] {
			if rng.float64() < 0.5 {
				continue
			}
			grids[face][index] = 0.1 + rng.float64()
			litLeaves[face*n*n+index] = true
		}
	}
	cube, err := NewCubeMap(n, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-10)

	hit := map[int]bool{}
	for sample := 0; sample < 80000; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		if !(pdf > 0) {
			t.Fatal("sampled direction with non-positive PDF")
		}
		face, u, v := locateDirection(direction)
		leaf := face*n*n + coordinateIndex(-v, n)*n + coordinateIndex(u, n)
		hit[leaf] = true
		if !litLeaves[leaf] {
			t.Fatalf("sampled black pixel leaf %d", leaf)
		}
	}
	for leaf := range litLeaves {
		if !hit[leaf] {
			t.Fatalf("lit pixel leaf %d was never sampled", leaf)
		}
		face, i, j := sampler.leafLocation(leaf)
		u0, u1, v0, v1 := cube.pixelBounds(i, j)
		direction := faces[face].direction((u0+u1)/2, (v0+v1)/2)
		if !(sampler.PDF(direction) > 0) {
			t.Fatalf("lit pixel leaf %d reports zero PDF", leaf)
		}
	}

	// Every black pixel reports exactly zero probability.
	for leaf := 0; leaf < FaceCount*n*n; leaf++ {
		if litLeaves[leaf] {
			continue
		}
		face, i, j := sampler.leafLocation(leaf)
		u0, u1, v0, v1 := cube.pixelBounds(i, j)
		direction := faces[face].direction((u0+u1)/2, (v0+v1)/2)
		if sampler.PDF(direction) != 0 {
			t.Fatalf("black pixel leaf %d reports non-zero PDF", leaf)
		}
	}
}

func TestSinglePixelFollowsSolidAngleArea(t *testing.T) {
	n := 4
	grids := [FaceCount][]float64{}
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, n*n)
	}
	const litFace, litI, litJ = FacePosZ, 1, 2
	grids[litFace][litJ*n+litI] = 2
	cube, err := NewCubeMap(n, grids)
	if err != nil {
		t.Fatal(err)
	}
	sampler := NewSampler(cube, 1e-10)

	u0, u1, v0, v1 := cube.pixelBounds(litI, litJ)
	pixelOmega := solidAngleRect(u0, u1, v0, v1)

	// Bucket u into strips; expected mass is the exact solid-angle fraction of
	// each strip, so this directly checks conformance to the area distribution.
	const strips = 10
	counts := make([]int, strips)
	rng := newLCG(777)
	const samples = 40000
	for sample := 0; sample < samples; sample++ {
		direction, pdf := sampler.Sample(rng.float64(), rng.float64(), rng.float64())
		face, u, v := locateDirection(direction)
		if face != litFace || coordinateIndex(u, n) != litI || coordinateIndex(-v, n) != litJ {
			t.Fatalf("sample escaped the only lit pixel: face=%d u=%g v=%g", face, u, v)
		}
		expectedPDF := 1 / pixelOmega
		nearlyEqual(t, pdf, expectedPDF, 1e-12, "single-pixel PDF")
		strip := int((u - u0) / (u1 - u0) * strips)
		if strip == strips {
			strip--
		}
		counts[strip]++
	}
	for strip := 0; strip < strips; strip++ {
		su0 := u0 + float64(strip)/strips*(u1-u0)
		su1 := u0 + float64(strip+1)/strips*(u1-u0)
		want := solidAngleRect(su0, su1, v0, v1) / pixelOmega
		got := float64(counts[strip]) / samples
		if math.Abs(got-want) > 0.02 {
			t.Fatalf("strip %d area fraction %.4f, want %.4f", strip, got, want)
		}
	}
}
