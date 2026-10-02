# atlas-packer

Deterministic fixed-page rectangle packer in TypeScript (Node.js built-in
type stripping, zero dependencies). Packs sprites with per-item margins and
inter-item spacing into fixed-size pages, allows 90° rotation, supports
pinned (fixed) items, produces atomic move plans for edits, and double-checks
every layout with an independent pixel-occupancy verifier built on a
hand-rolled RGBA image (no Canvas, no image libraries).

## Layout

- `src/types.ts` — data model (items, placements, move plans, proofs)
- `src/geometry.ts` — footprint math: `sprite + 2*(margin + ceil(spacing/2))`
- `src/maxrects.ts` — deterministic MAXRECTS bin (best-short-side-fit)
- `src/packer.ts` — heuristic multi-page pack, fixed items, oversized rejection
- `src/exact.ts` — branch & bound: optimality certificate for small inputs
- `src/update.ts` — add/remove/resize → minimal-move atomic plan
- `src/uv.ts` — UV ↔ page mapping (rotation-correct)
- `src/rgba.ts` — self-built RGBA image (fill/count/ascii)
- `src/verifier.ts` — independent raster + analytic separation checks
- `demo/demo.ts` — runnable sample
- `test/*.test.ts` — `node:test` suites

## Guarantees

- **Spacing counts**: capacity is accounted in footprint space, so margins
  and spacing can never be "forgotten".
- **Deterministic**: stable item order and tie-breaking; identical inputs
  give byte-identical layouts.
- **No overlap, verified twice**: a raster verifier paints every footprint
  into a fresh RGBA page image (any doubly-claimed pixel is an error) and an
  analytic checker validates pairwise separation `>= marginA+marginB+spacing`.
- **Oversized fails loudly**: items that cannot fit the page in any allowed
  orientation land in `unpacked` with reason `too-large`; nothing is ever
  placed out of bounds.
- **Optimality evidence**: with ≤10 movable items a branch & bound search
  certifies `proof.status === 'optimal'`; larger inputs return
  `{ status: 'heuristic', lowerBound, upperBound }`.
- **Atomic updates**: `planUpdate` keeps every still-valid placement (minimal
  moves), repacks only added/resized/evicted items, and verifies the result
  before publishing — on failure the previous layout is returned untouched.

## Run

```sh
npm test     # node --test test/*.test.ts
npm run demo # node demo/demo.ts
```

Requires Node ≥ 23.6 (type stripping is enabled by default). No install step;
there are no dependencies.

## Sample run (`npm run demo`)

```
=== pack ===
pages: 1, proof: {"status":"optimal","pages":1}
  grass     page=0 x=10 y=14 w=4 h=4 rotated=false
  hero      page=0 x=2 y=2 w=12 h=8 rotated=false
  logo      page=0 x=22 y=2 w=8 h=8 rotated=false
  rock      page=0 x=17 y=20 w=5 h=5 rotated=false
  tree      page=0 x=1 y=13 w=6 h=10 rotated=false
  ui-panel  page=0 x=17 y=12 w=14 h=6 rotated=false
  UNPACKED giant: too-large
verify: OK
=== update: add "bush", resize "rock", remove "grass" ===
moves: 4 stay, 3 changed
  add    bush      -> page=0 x=1 y=25 w=6 h=6
  remove grass     -> -
  move   rock      -> page=0 x=9 y=20 w=7 h=7
verify after update: OK
```

## Test results (`npm test`)

35 tests, all passing:

```
ℹ tests 35
ℹ suites 0
ℹ pass 35
ℹ fail 0
```

Coverage highlights:

- `test/pack.test.ts` — spacing charged into capacity, determinism,
  rotation allowed/forbidden, oversized rejection, fixed anchors and
  conflicts, per-item margins, multi-page spill, input validation.
- `test/exact.test.ts` — branch & bound optimality certificate on small
  inputs, lower/upper bounds + `heuristic` label on larger ones, fixed
  obstacles, minimal page count, node-budget fallback.
- `test/update.test.ts` — minimal-move updates, atomic failure (previous
  layout published untouched), fixed items never move, oversized resize
  rejected without overlap, move-plan sanity.
- `test/uv.test.ts` — rotation-correct UV mapping, inverse mapping,
  corners inside the box.
- `test/verifier.test.ts` — RGBA image primitives, overlap/spacing/
  bounds/rotation/fixed-item violation detection, edge-clipped margins.
