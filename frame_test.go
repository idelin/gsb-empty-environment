package taskpkg

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func testFrameLUT(t *testing.T, opts ...Option) *LUT {
	t.Helper()
	const sizeR, sizeG, sizeB = 3, 4, 2
	matrix := [3][3]float64{
		{0.8, -0.2, 0.3},
		{0.1, 1.1, -0.25},
		{-0.4, 0.2, 0.9},
	}
	offset := [3]float64{0.05, -0.2, 0.3}
	table := affineTable(sizeR, sizeG, sizeB, RedFastest, [3]float64{0, 0, 0},
		[3]float64{1, 1, 1}, matrix, offset)
	lut, err := NewLUT(sizeR, sizeG, sizeB, table, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return lut
}

func TestApplyFrame32StrideAlphaAndInPlace(t *testing.T) {
	lut := testFrameLUT(t)
	// Two rows, two pixels per row, RGBA interleaved (stride 4).
	// Alpha values are deliberately outside the LUT domain; they must not be
	// validated, graded, or changed.
	frame := []float32{
		0.1, 0.2, 0.3, -5,
		0.6, 0.7, 0.8, float32(math.NaN()),
		0.9, 0.1, 0.4, 100,
		0.3, 0.5, 0.9, 0.25,
	}
	original := append([]float32(nil), frame...)

	want := make([]RGB, 4)
	for i := 0; i < len(want); i++ {
		base := i * 4
		var err error
		want[i], err = lut.Sample(
			float64(original[base+0]),
			float64(original[base+1]),
			float64(original[base+2]),
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := lut.ApplyFrame32(frame, 2, 2, 4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		base := i * 4
		assertRGBClose32(t, frame[base:base+3], want[i], 1e-6, "float32 pixel")
		if math.Float32bits(frame[base+3]) != math.Float32bits(original[base+3]) {
			t.Fatalf("alpha at pixel %d changed: bits %08x -> %08x",
				i, math.Float32bits(original[base+3]), math.Float32bits(frame[base+3]))
		}
	}
}

func assertRGBClose32(t *testing.T, got []float32, want RGB, tol float64, msg string) {
	t.Helper()
	if math.Abs(float64(got[0]-float32(want.R))) > tol ||
		math.Abs(float64(got[1]-float32(want.G))) > tol ||
		math.Abs(float64(got[2]-float32(want.B))) > tol {
		t.Fatalf("%s: got (%v,%v,%v), want (%v,%v,%v), tol %g",
			msg, got[0], got[1], got[2], want.R, want.G, want.B, tol)
	}
}

func TestApplyFrame64PaddingAndGeneric(t *testing.T) {
	lut := testFrameLUT(t)
	const stride = 9
	frame := make([]float64, 5*stride)
	rng := rand.New(rand.NewSource(505))
	for i := 0; i < 5; i++ {
		frame[i*stride+0] = rng.Float64()
		frame[i*stride+1] = rng.Float64()
		frame[i*stride+2] = rng.Float64()
		for k := 3; k < stride; k++ {
			frame[i*stride+k] = float64(1000 + i*10 + k)
		}
	}
	original := append([]float64(nil), frame...)
	want := make([]RGB, 5)
	for i := range want {
		var err error
		want[i], err = lut.Sample(
			original[i*stride+0],
			original[i*stride+1],
			original[i*stride+2],
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := ApplyFrame(lut, frame, 5, 1, stride); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		base := i * stride
		assertRGBClose(t, RGB{
			R: frame[base+0],
			G: frame[base+1],
			B: frame[base+2],
		}, want[i], 1e-12, "float64 pixel")
		for k := 3; k < stride; k++ {
			if frame[base+k] != original[base+k] {
				t.Fatalf("padding changed at pixel %d offset %d", i, k)
			}
		}
	}
}

func TestApplyPixelsAndApplyAlias(t *testing.T) {
	lut := testFrameLUT(t)
	pixels := []float32{
		0.1, 0.2, 0.3, 99,
		0.4, 0.5, 0.6, 88,
		0.7, 0.8, 0.9, 77,
	}
	original := append([]float32(nil), pixels...)
	if err := lut.ApplyPixels(pixels, 3, 4); err != nil {
		t.Fatal(err)
	}
	if pixels[3] != 99 || pixels[7] != 88 || pixels[11] != 77 {
		t.Fatalf("stride padding changed: %v", pixels)
	}
	for _, base := range []int{0, 4, 8} {
		var err error
		want, err := lut.Sample(
			float64(original[base+0]),
			float64(original[base+1]),
			float64(original[base+2]),
		)
		if err != nil {
			t.Fatal(err)
		}
		assertRGBClose32(t, pixels[base:base+3], want, 1e-6, "ApplyPixels")
	}

	second := []float32{0.2, 0.4, 0.6, 7}
	before := append([]float32(nil), second...)
	if err := lut.Apply(second, 1, 1, 4); err != nil {
		t.Fatal(err)
	}
	want, err := lut.Sample(float64(before[0]), float64(before[1]), float64(before[2]))
	if err != nil {
		t.Fatal(err)
	}
	assertRGBClose32(t, second[:3], want, 1e-6, "Apply")
	if second[3] != 7 {
		t.Fatalf("alpha through Apply changed: %v", second[3])
	}
}

func TestFrameFailureIsAtomic(t *testing.T) {
	lut := testFrameLUT(t, Reject)
	const stride = 4
	frame := []float32{
		0.2, 0.4, 0.6, 1.0,
		0.3, 0.5, 0.7, 0.0,
		1.02, 0.8, 0.9, 0.5, // invalid, late in the frame
	}
	original := append([]float32(nil), frame...)
	if err := lut.ApplyFrame(frame, 3, 1, stride); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("error = %v, want ErrOutOfRange", err)
	}
	if !reflect.DeepEqual(frame, original) {
		t.Fatalf("frame partially modified on rejection: %v -> %v", original, frame)
	}

	nanFrame := append([]float32(nil), original...)
	nanFrame[2*stride+0] = 0.5
	nanFrame[2*stride+1] = float32(math.NaN())
	nanFrame[2*stride+2] = 0.9
	nanOriginal := append([]float32(nil), nanFrame...)
	if err := lut.ApplyFrame(nanFrame, 3, 1, stride); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("error = %v, want ErrNonFinite", err)
	}
	if !sameFloat32Bits(nanFrame, nanOriginal) {
		t.Fatalf("frame partially modified on NaN: %v -> %v", nanOriginal, nanFrame)
	}
}

func sameFloat32Bits(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

func TestFrameLayoutFailuresDoNotModify(t *testing.T) {
	lut := testFrameLUT(t)
	frame := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	original := append([]float32(nil), frame...)

	cases := []struct {
		name              string
		width, height, st int
	}{
		{"negative width", -1, 1, 3},
		{"negative height", 1, -1, 3},
		{"stride too small", 2, 1, 2},
		{"short frame", 3, 1, 3},
	}
	for _, tc := range cases {
		if err := lut.ApplyFrame(frame, tc.width, tc.height, tc.st); !errors.Is(err, ErrInvalidFrame) {
			t.Errorf("%s: error = %v, want ErrInvalidFrame", tc.name, err)
		}
		if !reflect.DeepEqual(frame, original) {
			t.Fatalf("%s: frame modified after preflight failure", tc.name)
		}
	}

	if err := lut.ApplyFrame(nil, 1, 1, 3); !errors.Is(err, ErrInvalidFrame) {
		t.Errorf("unsupported frame type error = %v, want ErrInvalidFrame", err)
	}
}

func TestFrameClampAndAlphaIndependence(t *testing.T) {
	lut := testFrameLUT(t)
	// Alpha is NaN/out of range while RGB is out of range but clampable.
	frame := []float64{
		-2, 3, 0.5, float64(math.NaN()),
		0.5, -2, 3, -100,
	}
	original := append([]float64(nil), frame...)
	if err := lut.ApplyFrame64(frame, 2, 1, 4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		base := i * 4
		want, err := lut.Sample(
			clamp01(original[base+0]),
			clamp01(original[base+1]),
			clamp01(original[base+2]),
		)
		if err != nil {
			t.Fatal(err)
		}
		got := RGB{R: frame[base], G: frame[base+1], B: frame[base+2]}
		assertRGBClose(t, got, want, 1e-12, "clamped pixel")
		if math.Float64bits(frame[base+3]) != math.Float64bits(original[base+3]) {
			t.Fatalf("alpha changed at pixel %d", i)
		}
	}
}

func TestZeroSizedFrameIsNoOp(t *testing.T) {
	lut := testFrameLUT(t)
	frame := []float32{1, 2, 3}
	if err := lut.ApplyFrame(frame, 0, 3, 0); err != nil {
		t.Fatalf("zero-sized frame: %v", err)
	}
	if frame[0] != 1 || frame[1] != 2 || frame[2] != 3 {
		t.Fatalf("zero-sized frame was modified: %v", frame)
	}
}
