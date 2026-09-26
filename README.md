# Deterministic Fixed-Palette Indexed Quantizer

A small, dependency-free TypeScript implementation that turns an RGBA image
into a **fixed-size palette plus an index map**, with transparent pixels
handled separately and optional **Floyd–Steinberg error diffusion that never
crosses transparent barriers**.

- Pure TypeScript + Node.js built-in test runner (`node --test`).
- No Canvas APIs, no image-processing dependencies. All test images are
  hand-built RGBA pixel arrays.
- Integer fixed-point diffusion arithmetic → identical output on every run
  and platform.
- Node ≥ 22.18 / 23 runs `.ts` directly (type stripping); no build step.

## Run

```bash
npm test     # node --test ... (built-in runner)
npm run demo # node scripts/demo.ts
```

## Design

### Fixed-size palette, transparent pixels separate

- `buildPalette(image, { colorCount: K })` returns exactly `K` opaque colors
  (`src/palette.ts`). Transparent pixels (alpha `<= 128`) never enter the
  color set and **never consume an opaque palette slot**.
- `colorCount = 1` is defined: the single entry is the weighted mean color;
  an image with no opaque pixels yields a deterministic black palette.
- Fewer distinct opaque colors than `K` → entries are deterministically
  padded by repeating the last color, so the palette length is always `K`.

### Exact benchmark vs heuristic (no false optimality claims)

- When the number of distinct opaque colors is small (`<= maxExactColors`,
  default 10), the provably minimal SSE is found by weighted set-partition
  enumeration; each block's best integer representative is searched
  exhaustively over source colors and rounded subset means (`solveExact`).
- Otherwise a deterministic **median-cut** heuristic is used
  (`method: "median-cut"`, `certifiedBest: false`). When the exact baseline
  is also affordable it is computed and the relative gap is reported as
  `gapVsExact = (heuristicSSE - exactSSE) / exactSse`. The heuristic never
  claims to be optimal.

### Error diffusion with transparent barriers

Palette selection and diffusion are separate: `encodeIndexed(image, palette,
{ method })` takes an already chosen palette (`src/encoder.ts`).

Floyd–Steinberg weights are kept in fixed point (denominator 16) per RGB
channel. A transparent pixel carries no residual and error is only delivered
through opaque pixels (`canDeliver` in `src/diffusion-core.ts`):

- horizontal error only flows right into an opaque neighbor;
- diagonal error additionally requires both orthogonal "bridge" cells to be
  opaque, so error cannot cut diagonally across a one-pixel transparent wall.

### Chunked streaming without changing results

`DiffusionEncoder.processRows(rows)` produces output in row bands. All
cross-row state lives solely in the versioned frontier, so **any** sequence
of chunk sizes yields indices/residuals identical to the one-shot encoder
(tested across multiple chunkings in `tests/streaming.test.ts`).

### Save / restore frontier and color-space version

`encoder.saveFrontier()` / `toJSON()` capture the row cursor, the per-column
incoming errors, already-produced indices/residuals and
`colorSpaceVersion` (`srgb-rgb-fs-v1`). `DiffusionEncoder.restore` rejects
incompatible versions, dimensions and diffusion methods.

### Local pixel revision with true affected range

`revisePixels(image, indexed, edits)` (`src/revision.ts`) edits pixels on an
already-diffused image and recomputes **only** the downstream diffusion cone
— not merely the edited tiles. The cone is computed with one raster forward
sweep over the union of the old and new opacity topologies (so turning a
pixel transparent and vice versa is handled), then affected pixels are
recomputed in raster order reusing unaffected residuals. Tests assert the
cone result is identical to a full re-encode and that it does not cross the
transparent barrier.

### Reconstruction error

`reconstructionError(source, indexed)` (`src/reconstruction.ts`) reports
opaque-pixel RGB SSE/MSE, counts transparent pixels, verifies transparent
pixels reconstruct with zero alpha (`transparentLeaks`) and reports opacity
mismatches.

## Layout

```
src/types.ts            shared types
src/image.ts            hand-built RGBA images, opacity threshold/mask
src/color.ts            sRGB distance, deterministic nearest color
src/palette.ts          exact solver + deterministic median cut
src/diffusion-core.ts   fixed-point weights and transparent-barrier gate
src/encoder.ts          one-shot encode + chunked DiffusionEncoder
src/revision.ts         local edits + affected-cone recomputation
src/reconstruction.ts   RGBA rebuild + reconstruction error
scripts/demo.ts         runnable sample
tests/*.test.ts         node:test suites
examples/demo-output.txt  captured sample output
```
