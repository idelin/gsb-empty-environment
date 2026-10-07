# hit-test-lib

Scene-graph hit testing in TypeScript. Runs on Node >= 22.6 (native type
stripping); no dependencies.

## Model

- Each `SceneNode` carries a local 2D affine matrix, an optional local hit
  rectangle, an optional clip shape (rect or circle, in the node's own local
  space) and children. Later siblings render on top; children render on top
  of their parent.
- Nodes are immutable. `with*` helpers and `update(root, target, fn)` do
  path-copying, so an old root reference keeps the entire old transform
  chain (persistent snapshots).

## Hit testing (`hitTest`)

- Searches topmost-first (reverse render order), transforming the world
  point into each node's local space with the per-level inverse matrix.
- A singular (non-invertible) matrix makes the node and its whole subtree
  unhittable.
- Rect/circle containment is boundary-inclusive (with a 1e-9 tolerance).
- A node's clip must contain the point (in the clip owner's local space);
  otherwise the node and its subtree are pruned. Clips along the ancestor
  path therefore intersect — circle and rect clips compose naturally.

## Spatial index (`SpatialIndex`)

- Caches, per root snapshot, a conservative world AABB per node:
  `worldAABB(rect) ∩ worldAABB(ancestor clips…)`. Circle clips use the exact
  AABB of the transformed ellipse.
- Because updates are path-copying, changing an ancestor transform yields a
  new root whose table is built fresh — every affected descendant cache is
  invalidated — while tables of older snapshots keep serving the old chain.
- The AABB is only a candidate filter: `SpatialIndex.hitTest` confirms each
  candidate with `exactPathContains` (local geometry + clipping), so results
  are identical to the direct traversal `hitTest`.

## Tests

```
node --test        # Node >= 22.6
```

Covers forward-transform-generated points for rotation/scaling/nested
chains, boundary inclusivity, singular matrices, nested and mixed
circle∩rect clipping, snapshot invalidation, and a randomized check that
index candidates never miss an exactly-hittable node.
