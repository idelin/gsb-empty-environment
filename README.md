# affine-hit-test

TypeScript scene-graph hit-testing library: nodes carry an invertible 2D
affine matrix, a local rectangle and optional ancestor clipping; a
world-bbox spatial index accelerates candidate lookup while real hit
testing always runs on local geometry plus clipping.

## Layout

- `src/matrix.ts` — 2D affine matrices: compose, invert (`null` when
  singular), apply, translate/rotate/scale.
- `src/geometry.ts` — rects, bboxes, rect/circle clip shapes;
  boundary-inclusive containment (with a small epsilon for float
  round-trips).
- `src/node.ts` — `SceneNode`: local rect, local matrix, optional clip
  shape, children, and a `version` bumped on every mutation.
- `src/scene.ts` / `src/view.ts` — `Scene`, immutable `SceneSnapshot`
  (captures transform chains + topology), and the `TreeView` projection
  used by all queries (`liveView()` / `snapshotView(snap)`).
- `src/hit.ts` — `hitTest` / `collectHits`: topmost-first reverse
  traversal, inverse-transforming the point layer by layer. Singular
  matrices prune the whole subtree; ancestor rect/circle clips intersect
  along the path. `exactHit` re-checks a single node independently.
- `src/index.ts` — `SpatialIndex`: caches each node's effective world
  bbox (transformed local rect ∩ all ancestor clip bboxes), keyed by the
  ancestor version chain.

## Semantics

- **Order**: last sibling is topmost; children are tested before their
  parent, in reverse sibling order.
- **Singular matrices**: a node whose composed world matrix is singular
  cannot be hit, and neither can its descendants.
- **Boundary**: rect/circle containment is inclusive.
- **Clipping**: a node's clip shape constrains its descendants only, and
  all clips on the ancestor path apply (intersection).
- **Cache invalidation**: cache keys embed the version of every ancestor,
  so updating an ancestor transform invalidates the cached bboxes of the
  whole subtree. Entries computed under older versions are retained and
  keep serving snapshot views — old snapshots keep the old chains.
- **Index vs. geometry**: the index is conservative (bbox of a rotated
  rect, square bbox of a circle); `SpatialIndex.hit` re-checks candidates
  with `exactHit` and orders them like the brute-force traversal, so it
  never misses and never disagrees with `hitTest`.

## Usage

```ts
import {
  Scene, SceneNode, SpatialIndex, hitTest,
  multiply, translate, rotate, circleClip,
} from "affine-hit-test";

const root = new SceneNode();
const parent = new SceneNode({ clip: circleClip(0, 0, 50) });
const child = new SceneNode({
  rect: { x: 0, y: 0, width: 20, height: 20 },
  matrix: multiply(translate(30, 0), rotate(Math.PI / 4)),
});
root.addChild(parent);
parent.addChild(child);

const scene = new Scene(root);
const index = new SpatialIndex(scene);

index.hit(30, 0);            // topmost node via index + exact geometry
hitTest(root, 30, 0);        // brute-force traversal (same answer)

const snap = scene.snapshot();
parent.setMatrix(translate(100, 0));   // invalidates the subtree cache
index.hit(30, 0, snapshotView(snap));  // old snapshot keeps the old chain
```

## Tests

Requires Node >= 22.18 (native type stripping). Run:

```sh
npm test
```

Tests generate world points by forward-transforming local points to
verify rotation, scaling and nested clipping; a randomized cross-check
compares index candidates against direct inverse-transform traversal to
prove no hit is ever missed.
