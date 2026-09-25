# rgba-diff-align

Dependency-free TypeScript tool for comparing two RGBA images with an explicit
color tolerance, reporting connected diff regions, and exactly searching the
integer alignment offset. No Canvas filters, no image-processing libraries —
images are plain `{ width, height, data: Uint8Array }` objects built in code.

Requires Node.js >= 22.6 (runs TypeScript via `--experimental-strip-types`;
tests use the built-in `node:test` runner). With nvm: `nvm use` (see `.nvmrc`).

```sh
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## Semantics (explicit, nothing hidden)

- **Canvas**: image A sits at `(0,0)`, image B at offset `(dx,dy)`. The
  comparison canvas is the bounding box of the *union* of both footprints.
  Size differences are never cropped away — they surface as uncovered pixels.
- **Transparent pixels**: a pixel with `alpha === 0` compares as `(0,0,0,0)`
  (its RGB bytes are meaningless). Two transparent pixels are always equal.
- **Tolerance**: `tolerance: { rgb, alpha }` is a required, explicit argument
  (per-channel, 0..255). A covered pair differs when the RGB delta exceeds
  `tolerance.rgb` or the alpha delta exceeds `tolerance.alpha`.
- **RGB vs alpha**: tracked separately end-to-end — per pixel (`kind` is
  `'rgb' | 'alpha' | 'rgb+alpha'`), per region, and in totals
  (`rgbPixels` / `alphaPixels`, `maxRgbDiff` / `maxAlphaDiff`).
- **Uncovered regions**: canvas cells covered by only one image are separate
  diff kinds (`'a-only'` / `'b-only'`), cost `uncoveredPenalty` each
  (default 255), and keep missing evidence (`aValue`/`bValue` is `null` on the
  absent side). Cells covered by neither image (gaps) are ignored.
- **Per-pixel error**: `error = max(max(0, rgbDiff - tol.rgb), max(0, alphaDiff - tol.alpha))`.
- **Cost**: `cost(offset) = Σ pixel errors + uncoveredPenalty × uncovered`,
  with `uncovered = |A| + |B| − 2·|overlap|` (exact, O(1) — images are rectangles).

## Diff regions

`compareImages(a, b, { offset, tolerance })` labels 4-connected components of
the diff mask. Every region reports `bboxCanvas` plus `bboxA` / `bboxB`
(mapped back into each image's own coordinates, `null` where the region does
not touch that image), kind counts, and the full pixel list with per-pixel
coordinate pairs and pixel values.

## Alignment search

`searchAlignment(a, b, { range, tolerance })` evaluates every integer offset
in `[minDX..maxDX] × [minDY..maxDY]` and returns **all** offsets tied at the
minimum cost, plus diagnostics (`evaluated` / `pruned` / `totalOffsets`).

Pruning uses a multi-resolution lower bound and never misses an optimum:

- Each image is cleaned (transparent → `(0,0,0,0)`) and collapsed into a
  pyramid of per-channel `[min,max]` intervals over `2^level × 2^level` cells.
- For offset `o = s·q + r` (`s = 2^level`, `0 ≤ r < s` per axis), every fine
  pixel pair maps to a coarse cell pair at offset `−q` or `−q−1` per axis.
  Grouping pairs by that combo yields
  `LB(o) = penalty·uncovered + Σ_u w_u · intervalErr(c, c−q−u)` with exact
  combo weights `w_u`. Interval distance ≤ any point distance, so
  `LB(o) ≤ cost(o)` for every offset; at level 0 the bound is exact.
- Offsets are processed in ascending coarse-LB order, refined through finer
  levels, and exactly evaluated only when no bound rules them out. Ties are
  safe: pruning requires `LB > best` (strict).

`bruteForceAlignment` is the independent per-offset reference: it prices every
offset through the full `compareImages` path (no pyramid, no shared cost
code). Tests assert pruned search ≡ brute force on random images, and
`exactCost` (lean loop) ≡ `compareImages.cost` (full path).

## Files

- `src/rgba.ts` — RGBA image model, builders, tiles, seeded PRNG
- `src/diff.ts` — per-pixel semantics, `compareImages`, connected regions
- `src/align.ts` — `exactCost`, interval pyramid, `lowerBoundCost`,
  `searchAlignment`, `bruteForceAlignment`
- `test/diff.test.ts`, `test/align.test.ts` — `node:test` suites on
  hand-built RGBA mini-images
- `examples/demo.ts` — end-to-end sample (output below)

## Sample run

```
=== alignment search ===
range: dx in [-6, 6], dy in [-6, 6]
best cost: 16075
optimal offsets (all ties): (3,2)
diagnostics: 169 offsets, 11 evaluated exactly, 158 pruned by lower bounds (pyramid levels: 2)
brute-force reference: best=16075, optima=(3,2)
cross-check: MATCH

=== diff at offset (3,2) with tolerance rgb<=4 alpha<=4 ===
canvas (union, nothing cropped): x=0 y=0 16x12
diff pixels: 64 (rgb=1, alpha=1, A-only=62, B-only=0)
maxError=255, maxRgbDiff=138, maxAlphaDiff=135, cost=16075
connected regions: 3
  region #0: canvas=(0,0 16x12) A=(0,0 16x12) B=none pixels=62 aOnly=62
  region #1: canvas=(7,5 1x1) A=(7,5 1x1) B=(4,3 1x1) rgb=1 maxError=134
  region #2: canvas=(10,8 1x1) A=(10,8 1x1) B=(7,6 1x1) alpha=1 maxError=131
```

## Test coverage

- identical images → equal, zero cost
- one-pixel change → exactly located, mapped to both coordinate systems
- alpha vs RGB differences counted separately
- tolerance critical points (`delta == tol` passes, `tol + 1` fails)
- transparent semantics (both-transparent equal; one-sided → alpha diff)
- size differences never cropped; uncovered pixels carry missing evidence
- negative offsets → negative canvas origin, regions still mapped back
- same image from tiles in different orders → identical results
- connected regions split/merge correctly
- `exactCost` ≡ `compareImages.cost` (independent implementations)
- lower bound admissible on every offset, exact at level 0
- shifted sub-image → unique optimum; repeated texture → all 9 tied optima
- pruned search ≡ brute-force reference on 25 random cases (seeded PRNG)
- pruning actually prunes; `levels: 0` gives identical results
- tolerance decides alignment at the critical point
- different sizes → uncovered cost included, all tied placements reported
