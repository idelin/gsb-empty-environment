package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

func testConfig() Config {
	return Config{
		Radius:       3,
		SigmaSpatial: 1.5,
		SigmaRange:   0.25,
		Compression:  0.55,
		Peak:         1,
	}
}

func TestTiledMatchesNaive(t *testing.T) {
	const width, height = 13, 11
	rng := rand.New(rand.NewSource(7))
	pix := make([]float32, width*height*3)
	for i := range pix {
		pix[i] = float32(math.Exp(rng.NormFloat64()*2.5 + 1))
	}

	cfg := testConfig()
	want := ToneMapNaive(pix, width, height, cfg)
	for _, tileSize := range []int{2, 4, 5, 32} {
		got := ToneMapTiled(pix, width, height, tileSize, cfg)
		if len(got) != len(want) {
			t.Fatalf("tile %d: length %d, want %d", tileSize, len(got), len(want))
		}
		for i := range want {
			if diff := math.Abs(float64(got[i] - want[i])); diff > 1e-6 {
				t.Fatalf("tile %d, index %d: got %g, want %g", tileSize, i, got[i], want[i])
			}
		}
	}
}

func TestConstantFrameIsUniformAndFinite(t *testing.T) {
	const width, height = 17, 9
	pix := make([]float32, width*height*3)
	for i := 0; i < width*height; i++ {
		pix[i*3], pix[i*3+1], pix[i*3+2] = 0.4, 0.5, 0.6
	}

	got := ToneMapRGB(pix, width, height, testConfig())
	first := [3]float32{got[0], got[1], got[2]}
	maximum := float32(0)
	for i := 0; i < width*height; i++ {
		for channel := 0; channel < 3; channel++ {
			value := got[i*3+channel]
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				t.Fatalf("non-finite output at pixel %d channel %d", i, channel)
			}
			if absDiff(value, first[channel]) > 1e-5 {
				t.Fatalf("constant frame is not uniform at pixel %d: %v vs %v", i, got[i*3:i*3+3], first)
			}
			if value > maximum {
				maximum = value
			}
		}
	}
	if math.Abs(float64(maximum)-1) > 1e-5 {
		t.Fatalf("output peak = %g, want 1", maximum)
	}
}

func TestHotPixelHasNoNaNOrHaloSeam(t *testing.T) {
	const width, height = 15, 15
	pix := make([]float32, width*height*3)
	for i := range pix {
		pix[i] = 0.01
	}
	center := (height/2)*width + width/2
	pix[center*3], pix[center*3+1], pix[center*3+2] = 1e20, 1e20, 1e20

	cfg := testConfig()
	cfg.SigmaRange = 0.15
	got := ToneMapRGB(pix, width, height, cfg)

	maxValue := float64(0)
	for i, value := range got {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			t.Fatalf("non-finite output at index %d", i)
		}
		if value < 0 {
			t.Fatalf("negative output at index %d: %g", i, value)
		}
		if float64(value) > maxValue {
			maxValue = float64(value)
		}
	}
	if math.Abs(maxValue-1) > 1e-4 {
		t.Fatalf("brightest output channel = %g, want 1", maxValue)
	}

	// With a small range sigma the hot pixel must not lift its neighbours
	// outside the immediate one-pixel vicinity; a tiled run must not introduce
	// any additional seams either.
	tiled := ToneMapTiled(pix, width, height, 4, cfg)
	for i := range got {
		if math.Abs(float64(got[i]-tiled[i])) > 1e-6 {
			t.Fatalf("tile seam at index %d: %g vs %g", i, got[i], tiled[i])
		}
	}
}

func TestZeroLuminanceStaysZero(t *testing.T) {
	pix := []float32{
		0, 0, 0,
		0.25, 0.5, 0.75,
		0, 0, 0,
	}
	got := ToneMapRGB(pix, 3, 1, testConfig())
	for _, index := range []int{0, 1, 2, 6, 7, 8} {
		if got[index] != 0 {
			t.Fatalf("zero-luminance pixel produced %g at index %d", got[index], index)
		}
	}
}

func TestEdgeOrderIsNotInverted(t *testing.T) {
	const width, height = 20, 5
	pix := make([]float32, width*height*3)
	const split = width / 2
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := float32(0.02)
			if x >= split {
				value = 50
			}
			i := (y*width + x) * 3
			pix[i], pix[i+1], pix[i+2] = value, value, value
		}
	}

	cfg := testConfig()
	cfg.Compression = 0.4
	got := ToneMapRGB(pix, width, height, cfg)
	left := float64(got[(height/2*width+split-1)*3])
	right := float64(got[(height/2*width+split)*3])
	if !(right > left) {
		t.Fatalf("edge reversed: left %g, right %g", left, right)
	}
}

func TestFloat64AndGenericEntryPoint(t *testing.T) {
	pix := []float64{0.1, 0.2, 0.3, 1, 2, 3, 0, 0, 0}
	got := ToneMapF64(pix, 3, 1, testConfig())
	for i, value := range got {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("non-finite output at %d", i)
		}
	}

	generic := ToneMap([]float32{0.1, 0.2, 0.3, 1, 2, 3}, 2, 1, testConfig())
	if len(generic) != 6 {
		t.Fatalf("generic result has length %d", len(generic))
	}
}

func TestScalarArgumentsAndIntegerPeak(t *testing.T) {
	const width, height = 5, 3
	pix := make([]float32, width*height*3)
	for i := range pix {
		pix[i] = float32(0.01 + 0.01*float64(i))
	}

	cfg := testConfig()
	want := ToneMapRGB(pix, width, height, cfg)
	got := ToneMapNaive(pix, width, height, cfg.Radius, cfg.SigmaSpatial, cfg.SigmaRange, cfg.Compression, 1)
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Fatalf("scalar arguments differ at %d: %g vs %g", i, got[i], want[i])
		}
	}

	im := Image{Width: width, Height: height, Pix: pix}
	fromImage := ToneMap(im, cfg)
	if fromImage.Width != width || fromImage.Height != height {
		t.Fatalf("generic Image result has size %dx%d", fromImage.Width, fromImage.Height)
	}
}

func TestConfigAliasesAndCallerOptions(t *testing.T) {
	const width, height = 6, 4
	pix := make([]float32, width*height*3)
	for i := range pix {
		pix[i] = float32(0.05 * float64(i+1))
	}

	canonical := testConfig()
	aliases := Config{
		KernelRadius: 3,
		SigmaD:       1.5,
		SigmaR:       0.25,
		Compress:     0.55,
		OutputPeak:   1,
	}
	want := ToneMapRGB(pix, width, height, canonical)
	got := ToneMapRGB(pix, width, height, aliases)
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Fatalf("alias config differs at %d: %g vs %g", i, got[i], want[i])
		}
	}

	type callerOptions struct {
		Radius      int
		SigmaSpace  float64
		SigmaRange  float64
		Compression float64
		Peak        float64
	}
	fromCaller := ToneMap(pix, width, height, callerOptions{
		Radius: 3, SigmaSpace: 1.5, SigmaRange: 0.25, Compression: 0.55, Peak: 1,
	})
	for i := range want {
		if math.Abs(float64(fromCaller[i]-want[i])) > 1e-6 {
			t.Fatalf("caller options differ at %d: %g vs %g", i, fromCaller[i], want[i])
		}
	}
}

func TestBilateralBaseOfConstantFrame(t *testing.T) {
	logs := []float64{math.Log(0.5), math.Log(0.5), math.Log(0.5), math.Log(0.5)}
	base := bilateralAt(logs, 2, 2, 1, 0, testConfig().resolve())
	if math.Abs(base-math.Log(0.5)) > 1e-12 {
		t.Fatalf("base = %g, want %g", base, math.Log(0.5))
	}
}

func absDiff(a, b float32) float32 {
	if a > b {
		return a - b
	}
	return b - a
}
