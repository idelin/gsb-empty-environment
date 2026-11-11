# linscale

Package linscale resizes images with a separable Lanczos-3 filter in
linear-light, premultiplied-alpha space. It is intended to be called from an
image service's processing pipeline.

## Why

Filtering raw sRGB byte values darkens thin black/white features because the
sRGB encoding is non-linear; filtering straight (non-premultiplied) color next
to transparency drags transparent black into the edge and leaves a black
fringe. linscale fixes both by working in the right color space.

## Pipeline

1. Decode sRGB channels to linear light (lookup table) and premultiply alpha.
2. Separable horizontal then vertical Lanczos-3 convolution.
3. On downscale, widen the kernel support inversely with the scale so the
   filter acts as an anti-aliasing low-pass filter.
4. Clamp source sample indices at the borders (clamp-to-edge), with kernel
   weights normalized against the unclamped mass.
5. Unpremultiply and encode linear values back to sRGB; alpha is linear
   coverage and is never sRGB-encoded.

Large images run the vertical pass in independent 16-row stripes across a
worker pool. Both the serial and parallel paths execute the same arithmetic
with float64 accumulation, so output is bit-for-bit identical regardless of
worker count.

## Usage

```go
import "example.org/linscale"

dst, err := linscale.Resize(src, 1024, 768) // src is any image.Image
if err != nil {
        // dstWidth/dstHeight <= 0 or empty source
}
// dst is *image.NRGBA (straight alpha, sRGB)
```

## Tests

- `TestThinLineLuminance` / `TestBlackAndWhiteLines`: white and black thin
  lines keep correct linear luminance and are brighter than a naive
  sRGB-space scaler used as a reference.
- `TestTransparentEdgeNoBlackFringe` / `TestSemiTransparentEdgeColor`:
  semi-transparent and hard transparent edges keep their original hue with no
  black contamination.
- `TestParallelMatchesSerial` / `TestLargeImageParallelPath`: forced-parallel
  and serial outputs are byte-identical.
- `TestKernelWideningOnDownscale`, `TestEdgeClamp`, `TestIdentity`,
  `TestSRGBRoundTrip`, `TestPremultipliedInput`.

Run with `go test ./...` and `go test -race ./...`.
