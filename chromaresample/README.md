# chromaresample

Chroma relocation for planar YUV media pipelines. It converts a component
plane between two sampling grids (for example 4:4:4, 4:2:2 and 4:2:0) without
ever assuming that chroma and luma share a sampling center.

## Sampling model

Every plane carries its own rational sampling center on each axis. The center
is the offset, in reference-grid (normally luma) pixel units, of sample 0 from
the left/top edge of reference cell 0:

- 4:4:4 luma: `CenterX = CenterY = 0/1`
- MPEG-2 4:2:2 chroma: `CenterX = 1/2`, `CenterY = 0/1`
- MPEG-2 4:2:0 chroma: `CenterX = CenterY = 1/2`
- JPEG co-sited chroma: `CenterX = CenterY = 0/1`

A zero `Rational` (denominator 0) is rejected, so a center can never be
silently defaulted to the luma center.

## Reconstruction

Resampling is separable. On each axis the supplied kernel (`BoxKernel`,
`TriangleKernel`, `LanczosKernel`) is scaled by the output/input spacing ratio
and integrated over the input cells covered by each output cell. The box
kernel and triangle kernel use analytic antiderivatives; other kernels use
8-point Gauss–Legendre quadrature. Weights are normalized, out-of-range
coordinates use pixel-mirror boundaries, all accumulation stays in float64,
and the final write rounds ties to even and saturates to `uint8`.

## Example

```go
geo := chromaresample.Geometry{
    RefWidth: 1920, RefHeight: 1080,
    Src: chromaresample.Grid{1920, 1080,
        chromaresample.Rational{0, 1}, chromaresample.Rational{0, 1}},
    Dst: chromaresample.Grid{960, 540,
        chromaresample.Rational{1, 2}, chromaresample.Rational{1, 2}},
}
r, err := chromaresample.New(geo, chromaresample.BoxKernel{})
// ...
err = r.Relocate(dstCbPlane, srcCbPlane)
```

`RelocatePlanes` relocates Cb and Cr together; `RelocateFloat` exposes the
unrounded float64 result for analytic verification.
