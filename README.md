# conv

2D discrete convolution (correlation form) over integer images with integer
kernels, in pure Go.

## Features

- **Boundary modes**: `Zero` (zero padding), `Mirror` (reflect-101: index
  `-1` maps to `1`, edge pixel not repeated), `Circular` (periodic wrap).
- **Arbitrary kernel anchor**: the anchor may be any element of the kernel,
  so even-sized kernels and causal/asymmetric filters work naturally.
- **Separable acceleration**: `Convolve` factors the kernel with `Factor`
  and uses the two-pass fast path only when the kernel is *exactly*
  separable over the integers; otherwise it falls back to the direct sum.
- **Exact arithmetic**: all accumulation (including the separable
  intermediate pass) uses `math/big`, so results never overflow, even with
  `int64`-limit pixels and coefficients.

## API

```go
im, _ := conv.NewImage(w, h, pix)              // pix is row-major []int64
k, _ := conv.NewKernel(3, 3, 1, 1, coeffs)     // anchor (1,1)
out := conv.Convolve(im, k, conv.Mirror)       // auto fast path
out.At(x, y)                                   // *big.Int

col, row, ok := conv.Factor(k)                 // exact separability test
conv.ConvolveDirect(im, k, conv.Zero)          // force direct evaluation
conv.ConvolveSeparable(im, rowK, colK, b)      // explicit two-pass
```

The convolution is defined as
`out(x,y) = Σ k[kx,ky] · img(x+kx-anchorX, y+ky-anchorY)`.

## Tests

```
GO111MODULE=on go test ./...
```

Covers hand-computed cases for every boundary mode, even-sized kernels with
off-center anchors, single-row/column images, negative coefficients,
`MaxInt64`-magnitude overflow safety, and a randomized cross-check of the
fast path against direct evaluation.
