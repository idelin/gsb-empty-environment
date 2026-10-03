# marker-watershed

Marker-controlled watershed segmentation on integer heightmaps, in dependency-free
TypeScript (runs directly on Node ≥ 22.6 via type stripping — no build step, no
Canvas, no image-processing libraries).

## Semantics

- **Sources** are the user seeds plus one pseudo-source on every *seedless
  regional minimum*, so basins without a seed stay **unassigned (label 0)**.
- Flooding is a Dijkstra-style priority flood with the total order
  **`(level, dist, frontId, pixel)`**:
  - `level` = minimax height along the path from the source;
  - `dist` = geodesic steps since the last level increase (plateau distance);
  - `frontId` breaks exact ties — a plateau contested by several seeds is
    decided by this **global rule**, never by scan order, tile order or seed
    insertion order.
- Withdrawing a seed (including a "bridging" one) re-floods its old region so
  the remaining fronts **re-compete** it.

## Incremental tiled engine

`src/engine.ts` divides the image into tiles. A mutation (height edit, seed
add/move/withdraw) dirties only the affected tiles — for removed fronts, the
tiles of their committed region — and re-floods just that region with imports
from the clean committed border. If a plateau carries the effect across a tile
border, the dirty region **grows across tiles** until border states match the
committed map again. The fixpoint always equals the full reference flood
(`src/reference.ts`), which the fuzz tests verify after every single op.

- **Atomic commits**: with a `budget` option a job pauses mid-flood; while
  paused, queries keep returning the last fully labeled version — never a
  half-labeled map. `step()` resumes.
- **Recovery**: `serialize()` / `deserialize()` capture everything including a
  paused job (heap cursor included), so an interrupted run can be journaled
  and resumed to the identical result.
- **Versions**: every commit stores a label **patch**, **merge/split events**,
  **adjacency with boundary evidence** (pixel counts + sample border pixels)
  and job stats. `getLabelsVersion(v)` / `getLabelAt(x, y, v)` answer
  old-version queries by walking the patch chain backwards.
- Seed ids and positions are **validated before any mutation**
  (`DUPLICATE_SEED_ID`, `SEED_POSITION_CONFLICT`, `OUT_OF_BOUNDS`, ...).

## Validation images

`src/rgba.ts` builds tiny RGBA images in code and decodes them by hand
(integer luminance, alpha checked) — no Canvas filters, no image libraries.

## Layout

- `src/core.ts` — heap, regional minima, fronts, resumable flood step
- `src/reference.ts` — full-image priority-flood oracle
- `src/engine.ts` — tiled incremental engine, versions, events, adjacency
- `src/rgba.ts` — self-made RGBA terrain + manual decoder
- `src/ops.ts`, `src/samples.ts` — replayable op logs and the built-in sample
- `test/` — Node's built-in test runner (`node:test`)

## Run

Requires Node ≥ 22.6 (uses native type stripping; developed on v23.9).

```sh
npm test        # node --test "test/*.test.ts"
npm run sample  # prints the replayable sample transcript
```

## Sample transcript (excerpt)

```
image 32x24 tile=8
addSeed(A@8,8)  -> v1 patch=228px pops=448  tiles=7 exp=0 split(0->1)
addSeed(B@24,16)-> v2 patch=203px pops=384  tiles=6 exp=0 split(0->2)
addSeed(C@16,12)-> v3 patch=4px   pops=192  tiles=2 exp=1 split(1->3)
moveSeed(C->24,12)-> v4 patch=7px pops=192  tiles=3 exp=0
setHeights(3px) -> v5 patch=24px  pops=320  tiles=4 exp=1   # channel carved across tiles
removeSeed(C)   -> v6 patch=3px   pops=128  tiles=2 exp=0 merge(3->[0])
setHeights(1px) -> paused (still v6)                        # budget-limited
resume -> v7
adjacency 0|1:35 0|2:33
```
