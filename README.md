# edt-field

Exact Euclidean (squared) distance field over a 2D obstacle grid, in pure
TypeScript with zero dependencies. Requires Node.js >= 22.6 (runs the
TypeScript sources directly via type stripping; no build step).

## Features

- **Exact transform** — separable Felzenszwalb–Huttenlocher EDT; squared
  distances are exact integers, never chamfer approximations
  (`src/edt.ts`).
- **Nearest-source tracking** — every cell records the coordinates of its
  nearest obstacle; equidistant ties resolve stably by source coordinates
  (smaller `x`, then smaller `y`).
- **Incremental repair** — obstacle add/remove and region moves repair only
  the affected regions with a dynamic raise/lower wavefront
  (`src/session.ts`). Deleting an obstacle re-sources its *entire* Voronoi
  region, not just its direct neighbours.
- **Chunked, versioned storage** — 16x16 copy-on-write pages; commits
  publish atomically (readers never see a mixed state), old versions stay
  queryable, and unreferenced pages are reclaimed by `collectGarbage()`
  (`src/field.ts`).
- **Path clearance queries** — polylines are sampled along every segment
  (never just the endpoints) against the distance field
  (`src/clearance.ts`).
- **Hand-rolled RGBA decoding** — validation images are built and decoded
  byte-by-byte; no Canvas filters, no image-processing libraries
  (`src/rgba.ts`).
- **Negative world coordinates** — the grid window can start at any
  (possibly negative) origin; out-of-window queries return Infinity.

## Usage

```ts
import { DistanceField } from "./src/distanceField.ts";

const field = DistanceField.fromObstacles(48, 20, [[8, 6], [30, 4]]);
const v1 = field.retain(field.version);

const edit = field.beginEdit();
edit.setObstacle(8, 6, false);      // remove an obstacle
edit.moveRegion(30, 4, 8, 3, -14, 8); // translate a region of obstacles
// ... readers still see v1 here; nothing half-repaired is visible
const v2 = edit.commit();           // atomic publish

field.clearanceAt(v2, 4.5, 10.5);           // Euclidean clearance at a point
field.pathClearance(v2, [{x: 2.5, y: 2.5}, {x: 45.5, y: 17.5}]);
field.isPathClear(v2, path, 1.5);           // boolean corridor test

field.release(v1);
field.collectGarbage();             // reclaim pages only v1 referenced
```

## Scripts

```sh
npm test        # node --test over test/ (22 tests)
npm run example # renders an ASCII distance map + edit/version/GC demo
```

## Tests (`node:test`, all deterministic)

- `test/edt.test.ts` — diagonal squared distance exactly 2; empty grid ->
  Infinity; tie-breaks; separable transform vs brute force on 50 random
  grids.
- `test/incremental.test.ts` — deleting the main obstacle reassigns a large
  region to the second-nearest source (propagation far beyond direct
  neighbours); deleting the only obstacle -> Infinity; coordinate-stable
  ties after updates; region moves; 25x12 rounds of random
  add/remove/move vs exact brute force; incremental vs full rebuild.
- `test/versioning.test.ts` — negative-coordinate mapping; old versions
  stay queryable and no mixed state is ever published; cancel rolls back;
  copy-on-write page sharing and GC reclamation.
- `test/clearance.test.ts` — clearance values; path clearance samples the
  whole path (an obstacle mid-segment is caught even when both endpoints
  are far away); version-aware queries; multi-segment polylines.
- `test/rgba.test.ts` — hand-built RGBA images decoded to obstacles
  (alpha threshold + custom channel predicate); origin offsets into
  negative world coordinates; full-field brute-force comparison.
