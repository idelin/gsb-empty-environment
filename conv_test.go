package conv

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

func mustImage(t *testing.T, w, h int, pix []int64) *Image {
	t.Helper()
	im, err := NewImage(w, h, pix)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	return im
}

func mustKernel(t *testing.T, w, h, ax, ay int, coeffs []int64) *Kernel {
	t.Helper()
	k, err := NewKernel(w, h, ax, ay, coeffs)
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	return k
}

func checkVals(t *testing.T, got *BigImage, want []int64) {
	t.Helper()
	if len(want) != got.W*got.H {
		t.Fatalf("want slice length %d, image is %dx%d", len(want), got.W, got.H)
	}
	for i, w := range want {
		if got.Pix[i].Cmp(big.NewInt(w)) != 0 {
			x, y := i%got.W, i/got.W
			t.Errorf("at (%d,%d): got %s, want %d", x, y, got.Pix[i].String(), w)
		}
	}
}

// 3x3 image with a horizontal 1x3 kernel [1 0 -1], anchor in the middle.
// Hand-computed for all three boundary modes.
func TestBoundariesHandComputed(t *testing.T) {
	im := mustImage(t, 3, 3, []int64{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	})
	k := mustKernel(t, 3, 1, 1, 0, []int64{1, 0, -1})

	checkVals(t, Convolve(im, k, Zero), []int64{
		-2, -2, 2,
		-5, -2, 5,
		-8, -2, 8,
	})
	checkVals(t, Convolve(im, k, Mirror), []int64{
		0, -2, 0,
		0, -2, 0,
		0, -2, 0,
	})
	checkVals(t, Convolve(im, k, Circular), []int64{
		1, -2, 1,
		1, -2, 1,
		1, -2, 1,
	})
}

// 2x2 image, non-symmetric 2x2 kernel, anchor at (0,0).
func TestDirectEvenKernelHandComputed(t *testing.T) {
	im := mustImage(t, 2, 2, []int64{1, 2, 3, 4})
	k := mustKernel(t, 2, 2, 0, 0, []int64{1, 2, 3, 4})

	checkVals(t, ConvolveDirect(im, k, Zero), []int64{30, 14, 11, 4})
	checkVals(t, ConvolveDirect(im, k, Circular), []int64{30, 28, 22, 20})
	checkVals(t, ConvolveDirect(im, k, Mirror), []int64{30, 28, 22, 20})
}

func TestMirrorIndex(t *testing.T) {
	// reflect-101 for n=4: ... 2 1 | 0 1 2 3 | 2 1 0 ...
	cases := map[int]int{-6: 0, -5: 1, -4: 2, -3: 3, -2: 2, -1: 1, 0: 0, 1: 1, 2: 2, 3: 3, 4: 2, 5: 1, 6: 0, 7: 1, 8: 2}
	for in, want := range cases {
		if got := mirrorIndex(in, 4); got != want {
			t.Errorf("mirrorIndex(%d, 4) = %d, want %d", in, got, want)
		}
	}
	if got := mirrorIndex(-7, 1); got != 0 {
		t.Errorf("mirrorIndex(-7, 1) = %d, want 0", got)
	}
}

func TestWrapIndex(t *testing.T) {
	cases := map[int]int{-7: 1, -4: 0, -1: 3, 0: 0, 3: 3, 4: 0, 7: 3, 8: 0}
	for in, want := range cases {
		if got := wrapIndex(in, 4); got != want {
			t.Errorf("wrapIndex(%d, 4) = %d, want %d", in, got, want)
		}
	}
	if got := wrapIndex(-3, 1); got != 0 {
		t.Errorf("wrapIndex(-3, 1) = %d, want 0", got)
	}
}

func TestFactorSeparable(t *testing.T) {
	// Non-primitive rows: factorization must stay integral.
	k := mustKernel(t, 2, 2, 0, 0, []int64{2, 4, 3, 6})
	col, row, ok := Factor(k)
	if !ok {
		t.Fatal("expected separable")
	}
	if row[0] != 1 || row[1] != 2 || col[0] != 2 || col[1] != 3 {
		t.Errorf("got col=%v row=%v, want col=[2 3] row=[1 2]", col, row)
	}

	// Negative multipliers.
	k = mustKernel(t, 2, 2, 0, 0, []int64{-2, -4, 3, 6})
	col, row, ok = Factor(k)
	if !ok {
		t.Fatal("expected separable")
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			if col[i]*row[j] != k.Coeffs[i*2+j] {
				t.Errorf("col=%v row=%v does not reproduce %v", col, row, k.Coeffs)
			}
		}
	}

	// Zero kernel is separable.
	k = mustKernel(t, 3, 2, 1, 0, make([]int64, 6))
	if _, _, ok = Factor(k); !ok {
		t.Error("zero kernel should be separable")
	}
}

func TestFactorNonSeparable(t *testing.T) {
	k := mustKernel(t, 2, 2, 0, 0, []int64{1, 0, 0, 1})
	if _, _, ok := Factor(k); ok {
		t.Error("identity kernel is not separable")
	}
	if _, _, err := Split(k); err != ErrNonSeparable {
		t.Errorf("Split error = %v, want ErrNonSeparable", err)
	}
}

// Single-row and single-column images under every boundary mode.
func TestSingleRowAndColumnImages(t *testing.T) {
	row := mustImage(t, 5, 1, []int64{1, 2, 3, 4, 5})
	colImg := mustImage(t, 1, 5, []int64{1, 2, 3, 4, 5})
	kv := mustKernel(t, 1, 3, 0, 1, []int64{1, 1, 1}) // vertical 3-tap
	kh := mustKernel(t, 3, 1, 1, 0, []int64{1, 1, 1}) // horizontal 3-tap

	checkVals(t, Convolve(row, kv, Zero), []int64{1, 2, 3, 4, 5})
	checkVals(t, Convolve(row, kv, Mirror), []int64{3, 6, 9, 12, 15})
	checkVals(t, Convolve(row, kv, Circular), []int64{3, 6, 9, 12, 15})

	checkVals(t, Convolve(row, kh, Zero), []int64{3, 6, 9, 12, 9})
	checkVals(t, Convolve(row, kh, Mirror), []int64{5, 6, 9, 12, 13})
	checkVals(t, Convolve(row, kh, Circular), []int64{8, 6, 9, 12, 10})

	checkVals(t, Convolve(colImg, kv, Zero), []int64{3, 6, 9, 12, 9})
	checkVals(t, Convolve(colImg, kv, Mirror), []int64{5, 6, 9, 12, 13})
	checkVals(t, Convolve(colImg, kv, Circular), []int64{8, 6, 9, 12, 10})
}

// Negative coefficients and negative pixels, even-sized kernel with an
// off-center anchor: auto (fast path) must match the direct evaluation.
func TestEvenKernelOffCenterAnchor(t *testing.T) {
	im := mustImage(t, 4, 3, []int64{
		-1, 2, -3, 4,
		5, -6, 7, -8,
		9, -10, 11, -12,
	})
	// 4x2 separable kernel, anchor not at any "center".
	col := []int64{-2, 3}
	row := []int64{5, -1, 0, 2}
	coeffs := make([]int64, 8)
	for i, c := range col {
		for j, r := range row {
			coeffs[i*4+j] = c * r
		}
	}
	k := mustKernel(t, 4, 2, 3, 1, coeffs)
	for _, b := range []Boundary{Zero, Mirror, Circular} {
		if got, want := Convolve(im, k, b), ConvolveDirect(im, k, b); !got.Equal(want) {
			t.Errorf("boundary %s: fast path disagrees with direct", b)
		}
	}
}

// Pixel and coefficient magnitudes near int64 limits: intermediate sums
// overflow int64, so exactness proves big-integer accumulation.
func TestBigIntNoOverflow(t *testing.T) {
	m := int64(math.MaxInt64)
	im := mustImage(t, 2, 2, []int64{m, m, m, m})
	k := mustKernel(t, 2, 2, 0, 0, []int64{m, m, m, m}) // separable: [m m] x [m m]

	bm := big.NewInt(m)
	term := new(big.Int).Mul(bm, bm)
	want := new(big.Int).Mul(term, big.NewInt(4)) // out(0,0) = 4*m*m

	got := Convolve(im, k, Zero)
	if got.At(0, 0).Cmp(want) != 0 {
		t.Errorf("out(0,0) = %s, want %s", got.At(0, 0), want)
	}
	if !got.Equal(ConvolveDirect(im, k, Zero)) {
		t.Error("separable fast path disagrees with direct on huge values")
	}

	// Mixed signs with huge magnitudes.
	im2 := mustImage(t, 3, 3, []int64{m, -m, m, -m, m, -m, m, -m, m})
	k2 := mustKernel(t, 3, 3, 1, 1, []int64{m, -m, m, -m, m, -m, m, -m, m})
	for _, b := range []Boundary{Zero, Mirror, Circular} {
		if got, want := Convolve(im2, k2, b), ConvolveDirect(im2, k2, b); !got.Equal(want) {
			t.Errorf("boundary %s: fast path disagrees with direct on huge mixed values", b)
		}
	}
}

// Randomized cross-check: the auto path (and the explicit separable path)
// must always agree with direct evaluation.
func TestAutoMatchesDirectRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	bounds := []Boundary{Zero, Mirror, Circular}
	for trial := 0; trial < 300; trial++ {
		w := 1 + rng.Intn(6)
		h := 1 + rng.Intn(6)
		pix := make([]int64, w*h)
		for i := range pix {
			pix[i] = int64(rng.Intn(101) - 50)
		}
		im := mustImage(t, w, h, pix)

		kw := 1 + rng.Intn(4)
		kh := 1 + rng.Intn(4)
		coeffs := make([]int64, kw*kh)
		if trial%2 == 0 {
			// Build an exactly separable kernel from random vectors.
			cv := make([]int64, kh)
			rv := make([]int64, kw)
			for i := range cv {
				cv[i] = int64(rng.Intn(11) - 5)
			}
			for j := range rv {
				rv[j] = int64(rng.Intn(11) - 5)
			}
			for i := 0; i < kh; i++ {
				for j := 0; j < kw; j++ {
					coeffs[i*kw+j] = cv[i] * rv[j]
				}
			}
		} else {
			for i := range coeffs {
				coeffs[i] = int64(rng.Intn(19) - 9)
			}
		}
		k := mustKernel(t, kw, kh, rng.Intn(kw), rng.Intn(kh), coeffs)

		for _, b := range bounds {
			want := ConvolveDirect(im, k, b)
			if got := Convolve(im, k, b); !got.Equal(want) {
				t.Fatalf("trial %d boundary %s: Convolve != ConvolveDirect (kernel %dx%d anchor (%d,%d) separable=%v)",
					trial, b, kw, kh, k.AnchorX, k.AnchorY, trial%2 == 0)
			}
			if col, row, ok := Factor(k); ok {
				got := ConvolveSeparable(im,
					&Kernel1D{Coeffs: row, Anchor: k.AnchorX},
					&Kernel1D{Coeffs: col, Anchor: k.AnchorY}, b)
				if !got.Equal(want) {
					t.Fatalf("trial %d boundary %s: ConvolveSeparable != ConvolveDirect", trial, b)
				}
			}
		}
	}
}

// A non-separable kernel must fall back to the direct path and still be exact.
func TestNonSeparableFallback(t *testing.T) {
	im := mustImage(t, 3, 3, []int64{1, -2, 3, -4, 5, -6, 7, -8, 9})
	k := mustKernel(t, 2, 2, 1, 1, []int64{1, 0, 0, 1}) // not separable
	if _, _, ok := Factor(k); ok {
		t.Fatal("kernel unexpectedly separable")
	}
	for _, b := range []Boundary{Zero, Mirror, Circular} {
		if got, want := Convolve(im, k, b), ConvolveDirect(im, k, b); !got.Equal(want) {
			t.Errorf("boundary %s: fallback disagrees with direct", b)
		}
	}
}

func TestConstructorsValidate(t *testing.T) {
	if _, err := NewImage(0, 1, nil); err == nil {
		t.Error("expected error for zero width")
	}
	if _, err := NewImage(2, 2, []int64{1}); err == nil {
		t.Error("expected error for short pixel slice")
	}
	if _, err := NewKernel(2, 2, 2, 0, []int64{1, 2, 3, 4}); err == nil {
		t.Error("expected error for anchor outside kernel")
	}
	if _, err := NewKernel(2, 2, 0, 0, []int64{1, 2, 3}); err == nil {
		t.Error("expected error for short coefficient slice")
	}
}
