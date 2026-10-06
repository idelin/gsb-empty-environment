# layered-rect-visibility

TypeScript geometry library for layered axis-aligned rectangles:

1. **Opaque visibility decomposition** — given rectangles in layer order
   (bottom → top), compute for each rectangle the non-overlapping rectangular
   fragments not covered by any rectangle above it.
2. **Translucent compositing** — rectangles carry straight RGBA colors with
   exact rational channels; the renderer partitions the plane into maximal
   rectangles of constant composited color, computed exactly with rational
   ("fraction") arithmetic. Layer groups apply an overall opacity to their
   isolated buffer, and the persistent scene graph keeps old snapshots valid
   across local updates.

## Geometry conventions

- Rectangles are **half-open**: `[x0, x1) × [y0, y1)`. Edge-touching
  rectangles neither overlap nor occlude each other.
- Fragment lists are **canonically sorted** by `(y0, x0, y1, x1)` —
  reading order, top-to-bottom then left-to-right.
- Fragments are pairwise non-overlapping, within a layer and across layers.

## Color semantics

- Colors are exact rationals (`Fraction`, bigint-backed). Compositing uses
  premultiplied form internally, so Porter-Duff `over` needs no division and
  results are canonical:
  `a = at + ab·(1−at)`, `P = Pt + Pb·(1−at)`.
- A **group** composites its children (index 0 = bottom) over transparency
  into an isolated buffer, then the buffer is blended over the backdrop with
  the group's opacity (premultiplied scaling; SVG-style isolated groups).
- **Pruning**: while folding a group top-to-bottom, once the accumulated
  alpha reaches 1 everything below is skipped — fully occluded layers cannot
  contribute. Translucent layers are never pruned. Fully transparent cells
  are dropped from the output.

## Persistence

`Scene` nodes are immutable. `updateNode` / `replaceNode` / `removeNode` /
`insertChild` return new scenes that share unchanged subtrees by reference,
so any previously held snapshot keeps rendering identically.

## Verification (test/)

- `opaque.test.ts` — small integer grids (random + hand-picked): every unit
  pixel is checked against the topmost-layer oracle, fragments are disjoint
  and canonically ordered, and per-layer + total **area conservation** holds
  (fragment area == covered pixel count).
- `composite.test.ts` — unit-grid **fraction compositing**: every pixel's
  fragment color equals an independent point-sampling oracle's exact rational
  color; geometry (disjointness, coverage, area conservation) is checked on
  the same grid. Hand-computed cases pin down group-opacity and pruning
  semantics.
- `scene.test.ts` — local updates preserve old snapshots (re-rendering a
  kept snapshot is bit-identical) and share untouched subtrees by reference.

## Build & test

```sh
npm run build   # tsc -> dist/
npm test        # build + run all suites
```

Requires a local TypeScript compiler (developed against TypeScript 5.2,
Node 16+).
