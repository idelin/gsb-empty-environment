// Package conv implements 2D discrete convolution (correlation form) over
// integer images with integer kernels.
//
// Features:
//   - Boundary handling: zero padding, mirror (reflect-101), circular wrap.
//   - Arbitrary kernel anchor (not necessarily the kernel center), so
//     even-sized kernels are supported naturally.
//   - Automatic separable-kernel acceleration: the fast path is taken only
//     when the kernel is exactly separable over the integers.
//   - All accumulation is done with math/big integers, so intermediate
//     results (including the separable intermediate pass) never overflow.
package conv

import (
	"errors"
	"fmt"
	"math/big"
)

// Boundary selects how out-of-range pixel accesses are resolved.
type Boundary int

const (
	// Zero pads with zeros outside the image.
	Zero Boundary = iota
	// Mirror reflects indices about the image edge without repeating the
	// edge pixel (reflect-101): index -1 maps to 1, index n maps to n-2.
	Mirror
	// Circular wraps indices modulo the image size (periodic extension).
	Circular
)

func (b Boundary) String() string {
	switch b {
	case Zero:
		return "zero"
	case Mirror:
		return "mirror"
	case Circular:
		return "circular"
	}
	return "unknown"
}

// Image is a 2D grid of int64 pixels in row-major order.
type Image struct {
	W, H int
	Pix  []int64
}

// NewImage builds an image of size w*h. If pix is nil the image is zeroed;
// otherwise pix must have length w*h and is copied.
func NewImage(w, h int, pix []int64) (*Image, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("conv: invalid image size %dx%d", w, h)
	}
	if pix != nil && len(pix) != w*h {
		return nil, fmt.Errorf("conv: pixel slice length %d, want %d", len(pix), w*h)
	}
	p := make([]int64, w*h)
	copy(p, pix)
	return &Image{W: w, H: h, Pix: p}, nil
}

// At returns the pixel at (x, y), resolving out-of-range coordinates
// according to the boundary mode.
func (im *Image) At(x, y int, b Boundary) int64 {
	if x >= 0 && x < im.W && y >= 0 && y < im.H {
		return im.Pix[y*im.W+x]
	}
	switch b {
	case Zero:
		return 0
	case Mirror:
		return im.Pix[mirrorIndex(y, im.H)*im.W+mirrorIndex(x, im.W)]
	case Circular:
		return im.Pix[wrapIndex(y, im.H)*im.W+wrapIndex(x, im.W)]
	}
	return 0
}

// mirrorIndex maps any index into [0, n) by reflecting about the edges
// without duplicating the edge element (reflect-101).
func mirrorIndex(i, n int) int {
	if n <= 1 {
		return 0
	}
	for i < 0 || i >= n {
		if i < 0 {
			i = -i
		} else {
			i = 2*n - 2 - i
		}
	}
	return i
}

// wrapIndex maps any index into [0, n) modulo n.
func wrapIndex(i, n int) int {
	r := i % n
	if r < 0 {
		r += n
	}
	return r
}

// Kernel is a 2D integer kernel with an explicit anchor. The anchor is the
// kernel element aligned with the output pixel: out(x, y) accumulates
// Coeffs[ky*W+kx] * img(x+kx-AnchorX, y+ky-AnchorY). Anchors need not be
// centered, which also covers even-sized kernels.
type Kernel struct {
	W, H             int
	Coeffs           []int64
	AnchorX, AnchorY int
}

// NewKernel builds a w*h kernel; coeffs must have length w*h and is copied.
// The anchor must lie inside the kernel.
func NewKernel(w, h, anchorX, anchorY int, coeffs []int64) (*Kernel, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("conv: invalid kernel size %dx%d", w, h)
	}
	if len(coeffs) != w*h {
		return nil, fmt.Errorf("conv: coefficient slice length %d, want %d", len(coeffs), w*h)
	}
	if anchorX < 0 || anchorX >= w || anchorY < 0 || anchorY >= h {
		return nil, fmt.Errorf("conv: anchor (%d,%d) outside %dx%d kernel", anchorX, anchorY, w, h)
	}
	c := make([]int64, w*h)
	copy(c, coeffs)
	return &Kernel{W: w, H: h, Coeffs: c, AnchorX: anchorX, AnchorY: anchorY}, nil
}

// Kernel1D is a one-dimensional integer kernel with an anchor.
type Kernel1D struct {
	Coeffs []int64
	Anchor int
}

// BigImage holds exact convolution results as big integers.
type BigImage struct {
	W, H int
	Pix  []big.Int
}

func newBigImage(w, h int) *BigImage {
	return &BigImage{W: w, H: h, Pix: make([]big.Int, w*h)}
}

// At returns a copy of the value at (x, y).
func (b *BigImage) At(x, y int) *big.Int {
	return new(big.Int).Set(&b.Pix[y*b.W+x])
}

// Equal reports whether both images have the same size and values.
func (b *BigImage) Equal(o *BigImage) bool {
	if b.W != o.W || b.H != o.H {
		return false
	}
	for i := range b.Pix {
		if b.Pix[i].Cmp(&o.Pix[i]) != 0 {
			return false
		}
	}
	return true
}

// Convolve computes the convolution of img with k. When the kernel is
// exactly separable over the integers it is factored and evaluated with the
// two-pass fast path; otherwise the direct O(W*H*w*h) path is used. Both
// paths are exact (big-integer accumulation) and always agree.
func Convolve(img *Image, k *Kernel, b Boundary) *BigImage {
	if col, row, ok := Factor(k); ok {
		return ConvolveSeparable(img,
			&Kernel1D{Coeffs: row, Anchor: k.AnchorX},
			&Kernel1D{Coeffs: col, Anchor: k.AnchorY}, b)
	}
	return ConvolveDirect(img, k, b)
}

// ConvolveDirect evaluates the convolution sum directly with big-integer
// accumulation.
func ConvolveDirect(img *Image, k *Kernel, b Boundary) *BigImage {
	out := newBigImage(img.W, img.H)
	term := new(big.Int)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			acc := new(big.Int)
			for ky := 0; ky < k.H; ky++ {
				sy := y + ky - k.AnchorY
				for kx := 0; kx < k.W; kx++ {
					c := k.Coeffs[ky*k.W+kx]
					if c == 0 {
						continue
					}
					p := img.At(x+kx-k.AnchorX, sy, b)
					if p == 0 {
						continue
					}
					term.SetInt64(p)
					term.Mul(term, big.NewInt(c))
					acc.Add(acc, term)
				}
			}
			out.Pix[y*img.W+x].Set(acc)
		}
	}
	return out
}

// ConvolveSeparable evaluates a kernel given as the outer product
// col ⊗ row in two passes: a horizontal pass with row followed by a
// vertical pass with col. The intermediate image uses big integers, so the
// result is exact regardless of coefficient or pixel magnitudes.
func ConvolveSeparable(img *Image, row, col *Kernel1D, b Boundary) *BigImage {
	if len(row.Coeffs) == 0 || len(col.Coeffs) == 0 {
		panic("conv: empty 1D kernel")
	}
	// Horizontal pass: tmp(x, y) = sum_kx row[kx] * img(x+kx-anchor, y).
	tmp := newBigImage(img.W, img.H)
	term := new(big.Int)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			acc := new(big.Int)
			for kx, c := range row.Coeffs {
				if c == 0 {
					continue
				}
				p := img.At(x+kx-row.Anchor, y, b)
				if p == 0 {
					continue
				}
				term.SetInt64(p)
				term.Mul(term, big.NewInt(c))
				acc.Add(acc, term)
			}
			tmp.Pix[y*img.W+x].Set(acc)
		}
	}
	// Vertical pass: out(x, y) = sum_ky col[ky] * tmp(x, y+ky-anchor).
	out := newBigImage(img.W, img.H)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			acc := new(big.Int)
			for ky, c := range col.Coeffs {
				if c == 0 {
					continue
				}
				sy := y + ky - col.Anchor
				switch {
				case sy >= 0 && sy < img.H:
					acc.Add(acc, term.Mul(term.Set(&tmp.Pix[sy*img.W+x]), big.NewInt(c)))
				case b == Zero:
					// contributes nothing
				case b == Mirror:
					sy = mirrorIndex(sy, img.H)
					acc.Add(acc, term.Mul(term.Set(&tmp.Pix[sy*img.W+x]), big.NewInt(c)))
				case b == Circular:
					sy = wrapIndex(sy, img.H)
					acc.Add(acc, term.Mul(term.Set(&tmp.Pix[sy*img.W+x]), big.NewInt(c)))
				}
			}
			out.Pix[y*img.W+x].Set(acc)
		}
	}
	return out
}

// Factor reports whether k is exactly separable over the integers, i.e.
// whether its coefficient matrix equals the outer product col ⊗ row with
// integer vectors. If so it returns such vectors with
// len(col) == k.H and len(row) == k.W. The zero kernel is separable and
// yields zero vectors.
func Factor(k *Kernel) (col, row []int64, ok bool) {
	h, w := k.H, k.W
	at := func(i, j int) int64 { return k.Coeffs[i*w+j] }

	// Find a nonzero pivot element (i0, j0).
	i0, j0 := -1, -1
	for i := 0; i < h && i0 < 0; i++ {
		for j := 0; j < w; j++ {
			if at(i, j) != 0 {
				i0, j0 = i, j
				break
			}
		}
	}
	if i0 < 0 {
		return make([]int64, h), make([]int64, w), true
	}

	// Row vector: pivot row reduced by the gcd of its entries, so the
	// factorization stays integral whenever the matrix has rank 1.
	row = make([]int64, w)
	g := new(big.Int)
	tmp := new(big.Int)
	for j := 0; j < w; j++ {
		tmp.SetInt64(at(i0, j)).Abs(tmp)
		g = gcdBig(g, tmp)
	}
	gcd := g.Int64()
	for j := 0; j < w; j++ {
		row[j] = at(i0, j) / gcd
	}

	// Rank-1 check with big-integer cross products (int64 products could
	// overflow), then read off col[i] = K[i][j0] / row[j0].
	col = make([]int64, h)
	left, right := new(big.Int), new(big.Int)
	r0 := big.NewInt(row[j0])
	for i := 0; i < h; i++ {
		ki0 := big.NewInt(at(i, j0))
		for j := 0; j < w; j++ {
			left.Mul(big.NewInt(at(i, j)), r0)
			right.Mul(ki0, big.NewInt(row[j]))
			if left.Cmp(right) != 0 {
				return nil, nil, false
			}
		}
		q := new(big.Int)
		r := new(big.Int)
		q.QuoRem(ki0, r0, r)
		if r.Sign() != 0 {
			return nil, nil, false
		}
		if !q.IsInt64() {
			return nil, nil, false
		}
		col[i] = q.Int64()
	}
	return col, row, true
}

// ErrNonSeparable is returned by Split when a kernel has no exact integer
// factorization.
var ErrNonSeparable = errors.New("conv: kernel is not separable")

// gcdBig returns gcd(|a|, |b|). It avoids big.Int.GCD, which misbehaves
// with nil Bezout outputs on older Go releases.
func gcdBig(a, b *big.Int) *big.Int {
	x := new(big.Int).Set(a)
	y := new(big.Int).Set(b)
	for y.Sign() != 0 {
		r := new(big.Int).Mod(x, y)
		x, y = y, r
	}
	return x.Abs(x)
}

// Split factors k into col ⊗ row or returns ErrNonSeparable.
func Split(k *Kernel) (col, row []int64, err error) {
	col, row, ok := Factor(k)
	if !ok {
		return nil, nil, ErrNonSeparable
	}
	return col, row, nil
}
