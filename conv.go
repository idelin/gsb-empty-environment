// Package conv implements 2D discrete convolution of integer images with
// integer kernels.
//
// Supported features:
//   - boundary handling: zero padding, mirroring and circular wrapping;
//   - arbitrary kernel anchors (the anchor need not be at the kernel
//     center and may even lie outside the kernel);
//   - an exact separable-kernel fast path: a kernel is decomposed into
//     column/row vectors only when it is exactly separable over the
//     integers, otherwise the direct algorithm is used;
//   - arbitrary precision accumulation via math/big, so intermediate
//     results never overflow regardless of coefficient or pixel magnitude.
//
// The operation computed is correlation in the strict signal-processing
// sense (the kernel is not flipped), matching the "anchor" convention of
// image filtering libraries such as OpenCV's filter2D:
//
//	out[y][x] = sum_{ky,kx} K[ky][kx] * img[y+ky-AnchorY][x+kx-AnchorX]
package conv

import "math/big"

// BoundaryMode selects how out-of-bounds pixel accesses are resolved.
type BoundaryMode int

const (
	// BoundaryZero treats out-of-bounds pixels as zero.
	BoundaryZero BoundaryMode = iota
	// BoundaryMirror reflects the image at its borders, half-sample
	// symmetric: the border pixel itself is repeated (index -1 maps to 0,
	// -2 maps to 1, and so on).
	BoundaryMirror
	// BoundaryCircular wraps coordinates around the image, treating it as
	// periodic.
	BoundaryCircular
)

// Kernel is a 2D integer convolution kernel. Coeffs must be a non-empty
// rectangular matrix indexed as Coeffs[y][x]. AnchorX and AnchorY select
// the coefficient that is aligned with the output pixel; the anchor need
// not be at the center and may lie outside the kernel bounds.
type Kernel struct {
	Coeffs  [][]int64
	AnchorX int
	AnchorY int
}

// Convolve filters img with k, automatically using the separable fast path
// when k is exactly separable over the integers and falling back to the
// direct algorithm otherwise. The result has the same dimensions as img;
// each pixel is an exact arbitrary-precision integer.
func Convolve(img [][]int64, k Kernel, mode BoundaryMode) [][]*big.Int {
	k.validate()
	checkImage(img)
	if col, row, ok := k.separate(); ok {
		tmp := convolveDirectBig(toBig(img), col, 0, k.AnchorY, mode)
		return convolveDirectBig(tmp, row, k.AnchorX, 0, mode)
	}
	return convolveDirectBig(toBig(img), k.Coeffs, k.AnchorX, k.AnchorY, mode)
}

// ConvolveDirect filters img with k using the direct O(H*W*KH*KW)
// algorithm, never the separable fast path. It is useful as a reference
// and for benchmarking.
func ConvolveDirect(img [][]int64, k Kernel, mode BoundaryMode) [][]*big.Int {
	k.validate()
	checkImage(img)
	return convolveDirectBig(toBig(img), k.Coeffs, k.AnchorX, k.AnchorY, mode)
}

// Separable reports whether k is exactly separable over the integers, i.e.
// whether K[y][x] == col[y]*row[x] for some integer vectors col and row.
// Only exactly separable kernels qualify for the fast path in Convolve.
func (k Kernel) Separable() bool {
	k.validate()
	_, _, ok := k.separate()
	return ok
}

func (k Kernel) validate() {
	if len(k.Coeffs) == 0 || len(k.Coeffs[0]) == 0 {
		panic("conv: kernel must be non-empty")
	}
	w := len(k.Coeffs[0])
	for _, r := range k.Coeffs {
		if len(r) != w {
			panic("conv: kernel rows must all have the same length")
		}
	}
}

func checkImage(img [][]int64) {
	if len(img) == 0 {
		return
	}
	w := len(img[0])
	for _, r := range img {
		if len(r) != w {
			panic("conv: image rows must all have the same length")
		}
	}
}

// mapIndex resolves a possibly out-of-range coordinate i for an axis of
// length n. The boolean result is false when the coordinate lies outside
// the image and the boundary mode treats it as zero.
func mapIndex(i, n int, mode BoundaryMode) (int, bool) {
	if i >= 0 && i < n {
		return i, true
	}
	switch mode {
	case BoundaryZero:
		return 0, false
	case BoundaryMirror:
		period := 2 * n
		m := i % period
		if m < 0 {
			m += period
		}
		if m >= n {
			m = period - 1 - m
		}
		return m, true
	case BoundaryCircular:
		m := i % n
		if m < 0 {
			m += n
		}
		return m, true
	}
	panic("conv: unknown boundary mode")
}

func toBig(img [][]int64) [][]*big.Int {
	out := make([][]*big.Int, len(img))
	for i, r := range img {
		row := make([]*big.Int, len(r))
		for j, v := range r {
			row[j] = big.NewInt(v)
		}
		out[i] = row
	}
	return out
}

func newBigImage(h, w int) [][]*big.Int {
	out := make([][]*big.Int, h)
	for i := range out {
		row := make([]*big.Int, w)
		for j := range row {
			row[j] = new(big.Int)
		}
		out[i] = row
	}
	return out
}

// convolveDirectBig correlates a big-integer image with an int64 kernel
// whose anchor is (anchorX, anchorY). All accumulation is exact.
func convolveDirectBig(img [][]*big.Int, coeffs [][]int64, anchorX, anchorY int, mode BoundaryMode) [][]*big.Int {
	h := len(img)
	if h == 0 {
		return nil
	}
	w := len(img[0])
	kh := len(coeffs)
	kw := len(coeffs[0])
	out := newBigImage(h, w)
	term := new(big.Int)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			acc := new(big.Int)
			for ky := 0; ky < kh; ky++ {
				sy, okY := mapIndex(y+ky-anchorY, h, mode)
				if !okY {
					continue
				}
				for kx := 0; kx < kw; kx++ {
					c := coeffs[ky][kx]
					if c == 0 {
						continue
					}
					sx, okX := mapIndex(x+kx-anchorX, w, mode)
					if !okX {
						continue
					}
					term.Mul(img[sy][sx], big.NewInt(c))
					acc.Add(acc, term)
				}
			}
			out[y][x].Set(acc)
		}
	}
	return out
}
