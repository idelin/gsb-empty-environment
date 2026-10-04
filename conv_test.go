package conv

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func strImg(img [][]*big.Int) [][]string {
	out := make([][]string, len(img))
	for i, r := range img {
		row := make([]string, len(r))
		for j, v := range r {
			row[j] = v.String()
		}
		out[i] = row
	}
	return out
}

func eqStr(t *testing.T, got [][]*big.Int, want [][]string) {
	t.Helper()
	g := strImg(got)
	if len(g) != len(want) {
		t.Fatalf("row count: got %d want %d (%v)", len(g), len(want), g)
	}
	for i := range g {
		if len(g[i]) != len(want[i]) {
			t.Fatalf("row %d width: got %d want %d", i, len(g[i]), len(want[i]))
		}
		for j := range g[i] {
			if g[i][j] != want[i][j] {
				t.Fatalf("pixel (%d,%d): got %s want %s (full %v)", i, j, g[i][j], want[i][j], g)
			}
		}
	}
}

func TestDirectZeroPadding(t *testing.T) {
	img := [][]int64{{1, 2}, {3, 4}}
	k := Kernel{Coeffs: [][]int64{{1, 0}, {0, -1}}, AnchorX: 0, AnchorY: 0}
	eqStr(t, ConvolveDirect(img, k, BoundaryZero), [][]string{
		{"-3", "2"},
		{"3", "4"},
	})
}

func TestMirrorBoundary(t *testing.T) {
	img := [][]int64{{10, 20, 30}}
	k := Kernel{Coeffs: [][]int64{{1, 1, 1}}, AnchorX: 1, AnchorY: 0}
	// Half-sample symmetric: -1 -> 0, 3 -> 2.
	eqStr(t, ConvolveDirect(img, k, BoundaryMirror), [][]string{
		{"40", "60", "80"},
	})
}

func TestCircularBoundary(t *testing.T) {
	img := [][]int64{{10, 20, 30}}
	k := Kernel{Coeffs: [][]int64{{1, 1, 1}}, AnchorX: 1, AnchorY: 0}
	eqStr(t, ConvolveDirect(img, k, BoundaryCircular), [][]string{
		{"60", "60", "60"},
	})
}

func TestEvenKernelAnchor(t *testing.T) {
	img := [][]int64{{1, 2}, {3, 4}}
	k := Kernel{Coeffs: [][]int64{{1, 1}, {1, 1}}, AnchorX: 1, AnchorY: 1}
	eqStr(t, ConvolveDirect(img, k, BoundaryZero), [][]string{
		{"1", "3"},
		{"4", "10"},
	})
	// Same kernel, anchor at top-left: sums the 2x2 block down-right.
	k2 := Kernel{Coeffs: [][]int64{{1, 1}, {1, 1}}, AnchorX: 0, AnchorY: 0}
	eqStr(t, ConvolveDirect(img, k2, BoundaryZero), [][]string{
		{"10", "6"},
		{"7", "4"},
	})
}

func TestSingleRowImageTallKernelMirror(t *testing.T) {
	img := [][]int64{{1, 2, 3}}
	k := Kernel{Coeffs: [][]int64{{1}, {1}, {1}, {1}, {1}}, AnchorX: 0, AnchorY: 2}
	// Height 1: every mirrored row index resolves to row 0.
	eqStr(t, ConvolveDirect(img, k, BoundaryMirror), [][]string{
		{"5", "10", "15"},
	})
}

func TestNegativeCoefficients(t *testing.T) {
	img := [][]int64{{7, -3}}
	k := Kernel{Coeffs: [][]int64{{-2, 5}}, AnchorX: 0, AnchorY: 0}
	eqStr(t, ConvolveDirect(img, k, BoundaryZero), [][]string{
		{"-29", "6"},
	})
}

func TestAnchorOutsideKernel(t *testing.T) {
	img := [][]int64{{1, 2, 3}}
	// Anchor one step to the right of a 1x1 kernel shifts the image left.
	k := Kernel{Coeffs: [][]int64{{1}}, AnchorX: 1, AnchorY: 0}
	eqStr(t, ConvolveDirect(img, k, BoundaryZero), [][]string{
		{"0", "1", "2"},
	})
}

func TestBigIntNoOverflow(t *testing.T) {
	big62 := int64(1) << 62
	img := [][]int64{{big62}}
	k := Kernel{Coeffs: [][]int64{{big62}}}
	want := new(big.Int).Lsh(big.NewInt(1), 124)
	got := ConvolveDirect(img, k, BoundaryZero)
	if got[0][0].Cmp(want) != 0 {
		t.Fatalf("got %s want %s", got[0][0], want)
	}

	// Sum of two MaxInt64 values overflows int64.
	m := int64(^uint64(0) >> 1)
	img2 := [][]int64{{m, m}}
	k2 := Kernel{Coeffs: [][]int64{{1, 1}}, AnchorX: 0, AnchorY: 0}
	want2 := new(big.Int).Mul(big.NewInt(m), big.NewInt(2))
	got2 := ConvolveDirect(img2, k2, BoundaryZero)
	if got2[0][0].Cmp(want2) != 0 {
		t.Fatalf("got %s want %s", got2[0][0], want2)
	}
}

func TestSeparableDetect(t *testing.T) {
	cases := []struct {
		coeffs [][]int64
		want   bool
	}{
		{[][]int64{{2, 4, -6}, {3, 6, -9}}, true},
		{[][]int64{{1, 2}, {3, 5}}, false},
		{[][]int64{{0, 0}, {0, 0}}, true},
		{[][]int64{{0, 0}, {1, 2}}, true},
		{[][]int64{{1, 2, 3}}, true},     // 1 x w is always separable
		{[][]int64{{1}, {2}, {3}}, true}, // h x 1 is always separable
		{[][]int64{{6}}, true},
		{[][]int64{{-4, 8}, {2, -4}}, true},
		{[][]int64{{1, 0}, {0, 1}}, false},
	}
	for i, c := range cases {
		k := Kernel{Coeffs: c.coeffs}
		if got := k.Separable(); got != c.want {
			t.Errorf("case %d: Separable()=%v want %v (%v)", i, got, c.want, c.coeffs)
		}
	}
}

func TestSeparableReconstruction(t *testing.T) {
	// col and row entries are large enough that pairwise products used in
	// the rank-1 check exceed int64, exercising the big.Int path.
	col := []int64{1 << 20, -(1 << 15), 7}
	row := []int64{-(1 << 20), 3, 1 << 25, -5}
	coeffs := make([][]int64, len(col))
	for i := range coeffs {
		coeffs[i] = make([]int64, len(row))
		for j := range coeffs[i] {
			coeffs[i][j] = col[i] * row[j]
		}
	}
	k := Kernel{Coeffs: coeffs}
	c, r, ok := k.separate()
	if !ok {
		t.Fatal("expected separable")
	}
	for i := 0; i < len(col); i++ {
		for j := 0; j < len(row); j++ {
			p := new(big.Int).Mul(big.NewInt(c[i][0]), big.NewInt(r[0][j]))
			if p.Cmp(big.NewInt(coeffs[i][j])) != 0 {
				t.Fatalf("reconstruction mismatch at (%d,%d): %s != %d", i, j, p, coeffs[i][j])
			}
		}
	}
}

func randInt(r *rand.Rand, bits uint) int64 {
	v := int64(r.Uint64() & ((1 << bits) - 1))
	if r.Intn(2) == 0 {
		v = -v
	}
	return v
}

func TestFastPathMatchesDirect(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	modes := []BoundaryMode{BoundaryZero, BoundaryMirror, BoundaryCircular}
	for trial := 0; trial < 300; trial++ {
		h := 1 + r.Intn(5)
		w := 1 + r.Intn(5)
		img := make([][]int64, h)
		for i := range img {
			img[i] = make([]int64, w)
			for j := range img[i] {
				img[i][j] = randInt(r, 40)
			}
		}
		kh := 1 + r.Intn(4)
		kw := 1 + r.Intn(4)
		var coeffs [][]int64
		if r.Intn(2) == 0 {
			// Build an exactly separable kernel from random factors.
			cf := make([]int64, kh)
			rf := make([]int64, kw)
			for i := range cf {
				cf[i] = randInt(r, 20)
			}
			for j := range rf {
				rf[j] = randInt(r, 20)
			}
			coeffs = make([][]int64, kh)
			for i := range coeffs {
				coeffs[i] = make([]int64, kw)
				for j := range coeffs[i] {
					coeffs[i][j] = cf[i] * rf[j]
				}
			}
		} else {
			coeffs = make([][]int64, kh)
			for i := range coeffs {
				coeffs[i] = make([]int64, kw)
				for j := range coeffs[i] {
					coeffs[i][j] = randInt(r, 20)
				}
			}
		}
		k := Kernel{
			Coeffs:  coeffs,
			AnchorX: r.Intn(kw+2) - 1,
			AnchorY: r.Intn(kh+2) - 1,
		}
		for _, mode := range modes {
			got := Convolve(img, k, mode)
			want := ConvolveDirect(img, k, mode)
			gs, ws := strImg(got), strImg(want)
			if fmt.Sprint(gs) != fmt.Sprint(ws) {
				t.Fatalf("trial %d mode %d kernel %v anchor (%d,%d) img %v:\nfast   %v\ndirect %v",
					trial, mode, coeffs, k.AnchorX, k.AnchorY, img, gs, ws)
			}
		}
	}
}

func TestFastPathOnlyWhenExactlySeparable(t *testing.T) {
	// A kernel that is "almost" separable must not take the fast path.
	near := Kernel{Coeffs: [][]int64{{2, 4}, {3, 7}}} // 2*7 != 4*3
	if near.Separable() {
		t.Fatal("near-separable kernel wrongly reported separable")
	}
	// Separability is over the integers: [[1,2],[2,4]] is separable,
	// [[1,2],[2,5]] is not even though it is close.
	if !(Kernel{Coeffs: [][]int64{{1, 2}, {2, 4}}}).Separable() {
		t.Fatal("exactly separable kernel rejected")
	}
	if (Kernel{Coeffs: [][]int64{{1, 2}, {2, 5}}}).Separable() {
		t.Fatal("non-separable kernel accepted")
	}
}

func TestEmptyImage(t *testing.T) {
	k := Kernel{Coeffs: [][]int64{{1, 1}, {1, 1}}}
	if got := Convolve(nil, k, BoundaryZero); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
