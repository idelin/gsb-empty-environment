# layered-rect-geometry

TypeScript geometry library for layered, axis-aligned rectangles with
half-open bounds `[x0, x1) x [y0, y1)`.

## Part 1 — opaque layers: visible fragments

`visibleFragments(rects)` takes opaque rectangles in layer order (index 0 =
bottom) and returns, for each rectangle, the pairwise-disjoint rectangular
fragments not covered by any rectangle above it. Fragments are canonically
sorted (lexicographic on `x0, y0, x1, y1`); fully covered rectangles yield an
empty list.

```ts
import { rect, visibleFragments } from "./src";

const frags = visibleFragments([rect(0, 0, 4, 4), rect(2, 2, 6, 6)]);
// frags[0] = [{0,0,4,2}, {0,2,2,4}]  (sorted)
// frags[1] = [{2,2,6,6}]
```

## Part 2 — translucency: exact rational compositing

- `Rational` — exact BigInt-backed fractions; all colors/alphas are exact.
- Scene graph: `rectLayer(id, rect, color, alpha)` and
  `layerGroup(id, opacity, children)`; children are bottom-to-top and group
  opacity multiplies into every descendant.
- `flattenScene(root)` → `FlatLayer[]` with effective alphas.
- `compositeLayers(layers)` → `Fragment[]`: maximal disjoint rectangles of
  constant visible layer stack, each with the composited rational `color`
  (over a transparent background), coverage `alpha`, and `sources` — the
  contributing layer ids after occlusion pruning. Layers below a fully
  opaque stack are pruned; semi-transparent layers never prune.

### Persistence

The scene graph is immutable. `updateRectLayer`, `setGroupOpacity`, and
`removeNode` return a new root that shares untouched subtrees with the old
one, so every old snapshot stays valid and cheap to keep.

## Verification

`npm test` (runs `tsc` then the test suite):

- Part 1: randomized small integer grids; every unit pixel's visible owner
  is computed by brute force and compared against the fragment decomposition;
  fragment areas must sum to the owned pixel count (area conservation).
- Part 2: randomized scenes with rational alphas/colors and nested opacity
  groups; every unit pixel is composited exactly (no pruning) and compared
  against the covering fragment's color/alpha; recompositing only the kept
  `sources` must reproduce the same result; fragment areas must tile exactly
  the covered pixels.
- Scene: local updates leave old snapshots byte-identical and share
  untouched branches by reference.

Note: `node_modules/typescript` is expected to provide `tsc`
(`node node_modules/typescript/bin/tsc`); no other dependencies.
