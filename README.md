# dynamic-bvh

A dynamic **AABB BVH** (bounding volume hierarchy) for 3D — and 2D, by keeping
`z = 0` — written in TypeScript, runnable directly on Node.js ≥ 23.6
(native type stripping, zero dependencies, zero build step).

## Features

- **SAH build** — `BVH.build()` constructs the tree top-down with the
  Surface Area Heuristic: binned split evaluation (12 bins) across all
  three axes, minimizing `SA(L)·|L| + SA(R)·|R|` at every node.
- **Dynamic updates** — `insert()` descends with a Box2D-style SAH
  best-sibling search and refits ancestors; `remove()` detaches the leaf,
  collapses its parent and refits. `validate()` checks structural integrity.
- **Ray cast** — `raycast()` traverses near-first with a slab-test AABB
  rejection, pruning any subtree missed by the ray or farther than the
  current best hit. Per-query counters (`bvh.stats`) report exactly how
  many leaves were visited vs. skipped.
- **Exact geometry** — `Triangle.intersectRay()` is Möller–Trumbore.

## Layout

```
src/vec3.ts      3D vector helpers
src/aabb.ts      AABB: union, surface area, slab ray test
src/ray.ts       ray with precomputed inverse direction
src/bvh.ts       BVH core class: SAH build, insert/remove, raycast
src/triangle.ts  triangle + Möller–Trumbore intersection
examples/scene.ts  deterministic scene/ray generator + brute-force reference
examples/demo.ts   acceptance demo (1000 triangles, 100 rays)
test/bvh.test.ts   node:test suite
```

## Usage

```ts
import { BVH, Ray, Triangle } from './src/index.ts';

const triangles: Triangle[] = /* ... */;
const bvh = BVH.build(triangles, (t) => t.bounds);   // SAH build

const hit = bvh.raycast(new Ray(origin, dir), (t, ray) => t.intersectRay(ray));
// hit = { object, t } | null  — the nearest hit

console.log(bvh.stats.leafNodesVisited, bvh.stats.leavesSkipped);

bvh.insert(newTriangle);   // dynamic insert
bvh.remove(oldTriangle);   // dynamic remove
```

## Run

```sh
npm test     # node --test (9 tests)
npm run demo # acceptance scenario
```

## Acceptance results (this machine)

```
[build]  1000 triangles -> SAH BVH in ~70 ms
[query]  100 rays in ~15 ms (~0.15 ms/ray)
[prune]  avg leaves tested/ray: 0.8 / 1000
[prune]  leaf skip ratio: 99.92% (target > 95%)
[result] hits: 42/100, mismatches vs brute force: 0
[dynamic] inserted 1000 objects one-by-one, valid: true
[dynamic] removed 183 objects, size=817, valid: true
```

Every BVH query result is checked against an O(n) brute-force nearest-hit
reference — identical object and hit distance on all 100 rays — while
> 99.9% of leaf nodes are skipped via AABB rejection.
