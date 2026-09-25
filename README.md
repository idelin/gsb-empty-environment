# Exact triangulation of simple polygons with holes

Python 3.11, standard library only (`fractions`, `dataclasses`, `unittest`).
All predicates are exact rational sign tests — no floating point, no
epsilon anywhere.

## Files

- `geom.py` — exact predicates: `orient`, segment intersection (proper /
  collinear-overlap / any), `point_in_ring` (INSIDE / OUTSIDE / BOUNDARY),
  signed area, strict point-in-triangle.
- `triangulate.py` — `triangulate(outer, holes)` returning `Triangle`
  objects whose `source` field records, for each corner, the provenance
  `(ring, index)`: ring `0` is the outer ring, ring `k` the k-th input
  hole, `index` the vertex index in the original input ring.
- `validate.py` — `validate_triangulation(outer, holes, triangles)`:
  exact area conservation (sum of triangle areas == outer − holes),
  containment (no triangle interior enters a hole or leaves the outer
  ring), pairwise non-overlap, non-degeneracy (every triangle has
  strictly positive area — bridge-duplicate vertices never produce
  zero-area output).
- `main.py` — runnable demo (integer + rational coordinates, collinear
  vertices, two holes).
- `test_triangulate.py` — 30 unittest cases.

## Algorithm

1. **Validation.** Rings are cleaned (consecutive duplicates dropped),
   oriented (outer CCW, holes CW), and checked: simplicity, holes
   strictly inside the outer ring, holes pairwise disjoint and
   non-nested.
2. **Hole bridges.** Each hole (rightmost vertex first) is spliced into
   the outer ring by a bridge from its maximal-`(x, y)` vertex to the
   nearest *strictly visible* vertex of the current ring. Visibility is
   exact: the open segment must not properly cross or collinearly
   overlap any edge, must not pass through any vertex, and its midpoint
   must lie strictly inside the outer ring and strictly outside every
   hole. Both bridge endpoints are duplicated in the ring (weakly
   simple polygon).
3. **Ear clipping.** Convex ears (`orient > 0`, no vertex strictly
   inside) are clipped one at a time. Collinear vertices on a straight
   run are removed *without* emitting a triangle, so collinear input
   vertices and bridge duplicates can never yield zero-area triangles.

## Degenerate cases (defined, not epsilon-patched)

- consecutive duplicate vertices: silently dropped;
- collinear vertices on a straight run: legal, removed during clipping;
- 180° spike, repeated vertex, self-intersection, zero-area ring,
  hole touching/leaving the outer ring, nested or touching holes:
  rejected with `PolygonError`.

## Run

```
python3.11 main.py          # demo
python3.11 -m unittest -v   # tests
```
