# iso-contours

Marching-squares contour extraction for **integer scalar grids** with exact
**rational** intersection coordinates. Written in TypeScript with zero
runtime dependencies.

## Requirements

Node.js >= 23.6 (runs TypeScript directly via type stripping), or
Node.js >= 22.6 with `--experimental-strip-types`.

```sh
npm test        # node --test 'test/*.test.ts'
```

## Usage

```ts
import { extractContours } from './src/index.ts';

const { chains, rings } = extractContours(
  [
    [0, 0, 0],
    [0, 10, 0],
    [0, 0, 0],
  ],
  5,
);
// rings[0] === 4 points with exact rational coordinates
// e.g. { x: Rational(1/2), y: Rational(1) }, ...
```

Input may also be a flat buffer: `{ data, width, height }`, and the threshold
may be a `number` or `bigint`. `Rational.toNumber()` / `pointToNumberPair()`
convert exact coordinates to floats when needed.

## Extraction semantics

- **Inside test**: a vertex is inside iff `value > threshold` (strict).
  Vertices exactly on the threshold are outside, but crossings on adjacent
  edges may land exactly on them (`t = 0` or `t = 1`).
- **Exact intersections**: edge crossings are linearly interpolated and kept
  as normalized `bigint` fractions (`Rational`), so coordinates are exact.
- **Saddle cells**: resolved by the bilinear center value `(a+b+c+d)/4`
  compared against the threshold (exact integer comparison `sum` vs
  `4*threshold`). When the center equals the threshold exactly, a **fixed
  diagonal rule** applies: the contour connects along the main diagonal
  (top-left ↔ bottom-right), i.e. the segments wrap the top-right and
  bottom-left corners.
- **Shared edges**: every edge crossing is computed exactly once
  (`ContourExtractor.interpolationCount` counts actual interpolations) and
  reused by both adjacent cells.
- **Degeneracies**: zero-length segments (both endpoints collapsing onto an
  iso-valued vertex) are dropped, and coincident duplicate segments (flat
  edges at the threshold separating two inside regions) are emitted only
  once — no dangling duplicate pieces.
- **Joining**: segments are joined into maximal trails — open `chains`
  (endpoints on the grid boundary or at dead ends) and closed `rings`
  (stored without repeating the first point). At shared vertices with
  degree > 2 (iso-valued pinch points), segments are paired
  deterministically in insertion order.

## API

- `extractContours(grid, threshold)` → `{ chains, rings }` of
  `RationalPoint[]`.
- `extractSegments(grid, threshold)` → flat `Segment[]` (deduplicated).
- `ContourExtractor` — low-level class exposing `edgeCrossing()`,
  `generateSegments()` and the `interpolationCount` statistic.
- `Rational` — exact fraction (`num`/`den` bigints, normalized),
  `pointKey()`, `pointToNumberPair()`.
