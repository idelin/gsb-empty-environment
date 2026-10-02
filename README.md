# atlas-packer

Deterministic fixed-page rectangle (sprite/texture) packer in dependency-free
TypeScript, runnable directly on Node.js >= 23.6 (built-in type stripping —
no build step, no npm dependencies, tests use `node:test`).

## Features

- **Fixed-size pages, gap edges counted**: every texture carries a per-item
  margin (uniform or per-side). The *footprint* (texture + margin) is what
  consumes page capacity, so spacing is never "free".
- **90-degree rotation** per item, with per-item `rotatable: false` opt-out.
  `uvCorners()` maps the four source corners (TL, TR, BR, BL) through the
  rotation so renderers sample the atlas correctly.
- **Deterministic heuristic**: MaxRects best-short-side-fit with a total
  tie-breaking order (fit, page, y, x, orientation) and input-order-independent
  sorting. Identical inputs always produce byte-identical layouts.
- **Fixed textures**: `fixed: { page, x, y, rotated }` items never move;
  everything else packs around them.
- **Bounds & optimality evidence**: an area lower bound is always reported.
  When the movable-item count is <= `exactThreshold` (default 12), an exact
  branch-and-bound over normal-pattern candidate positions proves the minimal
  page count (`certificate: 'exact'`, `optimal` set, layout rebuilt to the
  optimum if the heuristic was worse). Larger instances return
  `[lower, upper]` with `certificate: 'heuristic'`. A layout that meets the
  lower bound is certified optimal regardless of size.
- **Atomic updates**: `applyChanges(layout, items, { add, remove, resize })`
  first minimizes moves of already-placed textures (fixed never move,
  unchanged keep their spot, resized stay if the new footprint still fits),
  then minimizes page count for the rest. It returns an explicit move plan
  (`keep` / `remove` / `place` / `move`). Any failure — invalid change,
  oversized texture, fixed item no longer fitting, verification failure —
  returns `ok: false` and publishes **no** coordinates.
- **Independent pixel-occupancy verifier**: `verifyLayout()` rasterizes every
  footprint into a real per-page pixel grid and flags overlaps, out-of-bounds,
  margin collisions, moved fixed items, illegal rotations and missing items.
  It shares no code with the packer and re-checks every layout before it is
  returned.
- **Self-built RGBA images**: `createImage` / `blit` / `renderPages` do manual
  per-pixel copies (no Canvas, no image libraries), and
  `verifyRenderedPages()` re-reads every texel plus the UV corners to prove
  rotation and UV mapping end-to-end.
- **Explicit oversize failure**: textures that cannot fit an empty page in any
  allowed orientation are reported as `unpacked` with reason `too-large` —
  never silently clipped or overflowed.

## Usage

```ts
import { pack, applyChanges, verifyLayout, renderPages, uvCorners } from './src/index.ts';

const { layout, bounds } = pack(
  [
    { id: 'hero', w: 24, h: 16, margin: 2 },
    { id: 'pin', w: 8, h: 8, fixed: { page: 0, x: 0, y: 0 } },
    { id: 'portrait', w: 10, h: 20, rotatable: false },
  ],
  { pageW: 64, pageH: 64 },
);
// layout.placements: [{ id, page, x, y, w, h, rotated, margin }, ...]
// layout.unpacked:   [{ id, reason: 'too-large' | 'no-space' | 'invalid', detail }]
// bounds: { lower, upper, optimal, certificate: 'exact' | 'heuristic' }

const update = applyChanges(layout, items, {
  add: [{ id: 'icon', w: 8, h: 8, margin: 1 }],
  remove: ['hero'],
  resize: [{ id: 'portrait', w: 12, h: 12 }],
});
if (update.ok) {
  // update.plan: atomic move ops; update.layout passed pixel verification.
}
```

## Run

```sh
npm test        # node --test (36 tests)
npm run sample  # end-to-end demo: pack, render, verify, atomic update
```

## Layout of the repo

- `src/types.ts` — item/placement/layout model, margins, footprints
- `src/packer.ts` — validation + deterministic MaxRects heuristic
- `src/exact.ts` — area lower bound + exact branch-and-bound feasibility
- `src/pack.ts` — `pack()`: heuristic, bounds, exact certificate, final verify
- `src/planner.ts` — `applyChanges()`: minimal-move atomic update plans
- `src/verify.ts` — independent pixel-occupancy verifier
- `src/image.ts` — RGBA images, rotation-aware blit, UV mapping, pixel readback
- `test/` — `node:test` suites
- `examples/run-sample.ts` — runnable demo
