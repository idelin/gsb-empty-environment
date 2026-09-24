# Dynamic BVH Index (Python 3.11, stdlib only)

A dynamic bounding-volume hierarchy (AABB tree) with exact arithmetic,
supporting insertion, deletion, movement, intersection queries and
k-nearest queries.

## Files

- `bvh.py` — `AABB` and `DynamicBVH` implementation
- `test_bvh.py` — unittest suite (27 tests, incl. randomized comparison
  against a brute-force reference)
- `demo.py` — runnable usage example

## Semantics

- **Exact arithmetic**: coordinates are `int` or `fractions.Fraction`.
  All predicates (intersection, containment, distance ordering) are exact;
  no epsilon tolerances anywhere. Nearest queries return *squared*
  distances, so results stay exact and ordering is unaffected.
- **Closed boxes**: boxes touching on a face/edge/corner intersect.
  Degenerate boxes (points, zero-extent slabs) are fully supported.
- **Determinism**: intersection results are sorted by id; nearest ties
  are broken by ascending id; internal heap/sibling ties use fixed rules.
- **Move** is remove+insert: after `move`, the object is unreachable in
  its old region.
- **Empty tree**: all queries return empty results; `remove`/`move` of a
  missing id raise `KeyError`; duplicate `insert` raises `ValueError`.
- **Invariants**: every internal node's box equals the exact union of its
  children's boxes; `check_invariants()` verifies structure, heights and
  parent pointers (used throughout the tests).

## API

```python
from bvh import AABB, DynamicBVH

tree = DynamicBVH()
tree.insert("a", AABB((0, 0), (1, 1)))     # or tree.insert("a", ((0,0),(1,1)))
tree.move("a", AABB((5, 5), (6, 6)))
tree.query(AABB((0, 0), (3, 3)))           # -> []  (sorted ids)
tree.nearest((5, 5), k=3)                  # -> [("a", 0), ...] (id, dist^2)
tree.remove("a")
tree.check_invariants()
```

## Run

```sh
python3.11 demo.py
python3.11 -m unittest test_bvh -v
```
