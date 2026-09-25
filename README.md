# detquant

Deterministic fixed-size palette quantization for RGBA images, in pure
TypeScript with **zero runtime dependencies** — no Canvas, no image
libraries. Tested with the Node.js built-in test runner.

## Requirements

- Node.js **>= 22.6** (runs TypeScript directly via type stripping;
  no build step, no dependencies to install).

```sh
npm test     # node --test test/*.test.ts
npm run sample
```

## Design

| Concern | Module |
| --- | --- |
| RGBA image / palette / indexed image types | `src/types.ts` |
| Versioned color spaces (`srgb-u8-v1`, `srgb-linear-v1`) | `src/colorspace.ts` |
| Palette selection (deterministic median cut) | `src/palette.ts` |
| Nearest-color lookup (deterministic tie-break) | `src/quantize.ts` |
| Error diffusion + serializable frontier | `src/diffuse.ts` |
| Tiled streaming, identical for any tile size | `src/stream.ts` |
| Exact small-domain optimum (partition enumeration) | `src/exact.ts` |
| Reconstruction error metrics | `src/errorMetrics.ts` |
| Affected region of a local pixel revision | `src/revision.ts` |
| End-to-end pipeline + gap report | `src/pipeline.ts` |

Palette selection and error diffusion are fully separate stages: the
palette is a plain value object, and the diffusion engine consumes any
palette plus any versioned color space.

### Transparency

- Pixels with `alpha === 0` are transparent. They get the reserved index
  `TRANSPARENT_INDEX = -1` and **never consume the opaque color budget**:
  they are excluded from palette fitting, from the exact baseline, and
  from every error statistic.
- Transparent pixels are **diffusion barriers**: they emit no error and
  swallow any error deposited into them, so error never crosses them.

### Determinism

No randomness, no wall-clock, fully specified tie-breaks everywhere
(median-cut box choice, sort order, nearest-color scan, partition
enumeration). Identical inputs produce byte-identical outputs.
`colorCount = 1` is defined (the mean of all opaque colors); an
all-transparent image yields an empty palette.

### Tiled streaming and frontier snapshots

Rows are processed in one global raster order and the Floyd–Steinberg
frontier (two error accumulator rows) is carried across tile boundaries,
so the output is bit-identical for any `tileHeight`. The frontier is
JSON-serializable (`snapshotState` / `restoreState`) and records the
`colorSpaceVersion`; restoring with a mismatched version fails loudly.
A stream paused after any tile resumes bit-exactly.

### Local pixel revision

`affectedRegion(image, rect, dither)` returns the exact set of pixels
whose quantized output may change after editing `rect` — a
downward-opening wedge clipped by image bounds and transparent barriers,
**not** just the modified tile. With dithering disabled it is exactly
`rect`. A test proves that re-quantizing after an edit changes nothing
outside the computed region.

### Exact baseline and the honesty rule

For small color domains, `exactBaseline` enumerates every set partition
of the distinct opaque colors into at most K clusters (restricted growth
strings) and scores each by weighted within-cluster squared error — the
true global optimum, since optimal palette colors are cluster centroids.
The heuristic (median cut) reports `gap = heuristicError - exactError`
and `claimsOptimal: false`; it never claims optimality. If the domain is
too large (partition count above budget) or the color space is not the
native u8 space, the baseline is `null` and no claim is made at all.

## Sample run (`npm run sample`)

Builds a 16x12 RGBA gradient in code with a transparent barrier column
and a transparent corner block, then exercises the whole pipeline:

```text
image: 16x12, opaque pixels: 171, transparent: 21

palette (opaque colors only):
  [0] rgb(57, 51, 54)
  [1] rgb(70, 179, 116)
  [2] rgb(203, 60, 142)
  [3] rgb(203, 200, 202)

indexed map (. = transparent):
00000000.2222222
00000020.0202222
00000002.2222222
00000002.2022222
00000020.2222222
01011111.1232323
10110102.2323233
11111111.1313332
11111113.3333333
...11111.1333333
...11131.3333333
...11113.3333333

reconstruction error (transparent pixels excluded):
  mse=4665.0351 rmse=68.3011 mae=32.0214 max=21780.0

tile-size invariance (hash of indices must be identical):
  tileHeight= 1  hash=dd619c35
  tileHeight= 2  hash=dd619c35
  tileHeight= 3  hash=dd619c35
  tileHeight= 4  hash=dd619c35
  tileHeight= 5  hash=dd619c35
  tileHeight=12  hash=dd619c35

frontier save/restore: resumed hash=dd619c35 uninterrupted hash=dd619c35 (saved 986 bytes of JSON, colorSpace=srgb-u8-v1)

determinism: run1=dd619c35 run2=dd619c35

revision at (5,3): affected spans per row (note the barrier column x=8 clips the wedge):
  y= 3  x=[5..7]
  y= 4  x=[4..7]
  y= 5  x=[3..7]
  y= 6  x=[2..7]
  y= 7  x=[1..7]
  y= 8  x=[0..7]
  y= 9  x=[3..7]
  y=10  x=[3..7]
  y=11  x=[3..7]

colors=1: palette=[[133,122,128]] rmse=125.7285

small domain (11 distinct opaque colors, K=2):
  heuristic palette: [[27,74,40],[174,85,63]]
  exact optimal palette: [[32,93,46],[203,54,59]]
  exact min error=75750.78571428571  heuristic error=85827  gap=10076.21428571429 (13.30%)
```

## Test results (`npm test`, node --test)

29 tests, all passing:

```text
ok 1 - transparent pixels are barriers: error does not cross them
ok 2 - frontier snapshot/restore is bit-exact
ok 3 - restore rejects unknown color space versions and bad shapes
ok 4 - dither disabled leaves the frontier untouched
ok 5 - exact baseline matches brute force on a tiny domain
ok 6 - K >= distinct colors gives zero exact error
ok 7 - transparent pixels are excluded from the exact baseline
ok 8 - oversized domains return null instead of a baseline
ok 9 - heuristic reports its gap and never claims optimality
ok 10 - palette is deterministic across runs
ok 11 - colorCount = 1 is defined: the mean of all opaque colors
ok 12 - colorCount = 0 or fractional is rejected
ok 13 - transparent pixels never consume the opaque color budget
ok 14 - all-transparent image yields an empty palette
ok 15 - fewer distinct colors than requested yields a smaller palette
ok 16 - same input produces byte-identical output across runs
ok 17 - colors = 1 is defined end to end
ok 18 - reconstruction error is computable and zero when the palette covers all colors
ok 19 - transparent pixels are excluded from every error statistic
ok 20 - tileHeight does not change pipeline output
ok 21 - a revision propagates far beyond its own tile
ok 22 - transparent barriers clip the propagation wedge
ok 23 - without dithering the affected region is exactly the rect
ok 24 - pixels outside the affected region are provably unchanged
ok 25 - tile height never changes the result
ok 26 - tile height never changes the result (no dither)
ok 27 - stream can pause after a tile and resume from a JSON snapshot
ok 28 - resuming with a mismatched color space version fails loudly
ok 29 - color space version changes the quantization result
# tests 29
# pass 29
# fail 0
```
