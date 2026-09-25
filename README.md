# Exact nearest-pair queries over editable polyline groups

Python 3.11, standard library only (`fractions`, `heapq`, `unittest`).

## Modules

- `geometry.py` — exact rational geometry.  All coordinates are
  `int`/`Fraction`; all distance comparisons use **squared** distances,
  so no square roots, no floats, and **no epsilon anywhere**.
- `tree.py` — versioned immutable AVL bounding-volume tree with exact
  node accounting (`NodePool`).
- `engine.py` — named groups of editable segments, per-group exclusion
  relation, radius thresholds, snapshots, cancellation, evidence.
- `tests/` — unittest suite, including a brute-force all-pairs oracle
  (`tests/oracle.py`) used by the event-interleaving cross-check.

## Defined semantics for degenerate cases

- **Zero-length segment** is a point; point/point, point/segment and
  segment/segment distances are all exact.
- **Crossing/touching segments** have `dist2 == 0` with exact rational
  closest-point parameters (e.g. `t = 1/3`).
- **Parallel equidistant segments** (including collinear overlaps) have
  a continuum of minimisers, computed exactly as the intersection of
  the line `s - lambda*t = w*` with the parameter box.  `mode="all"`
  reports the exact parameter interval; `mode="representative"`
  collapses it to the lexicographically smallest parameter pair
  (a stable, deterministic representative).
- **Shared polyline vertices**: evidence is deduplicated by exact
  geometry — the same closest point pair reported through two adjacent
  segments appears once, keeping the record with the smallest
  (group, segment, parameter) tuple.
- **Radius threshold**: a pair qualifies iff `dist2 <= radius**2`
  (boundary included); an empty result has `dist2 is None`.
- **Exclusions**: pairs of groups on the exclusion list are never
  considered; querying an excluded pair directly returns an empty
  result.  Intra-group queries are rejected (`ValueError`).

## Versioning and concurrency model

- Every edit (`set_segment` / `remove_segment`) builds a **new
  immutable root**; unchanged subtrees are shared by identity, so a
  local move only rebuilds the O(log n) path nodes (plus rotation
  nodes).  Rebalancing only ever *creates* nodes.
- Queries read immutable versions: an explicit `Snapshot` (historical
  queries, including the exclusion relation at snapshot time) or a
  private snapshot released when the query ends.
- In-flight queries can never touch reclaimed memory: nodes are
  reclaimed only by reference counting/GC, and a snapshot pins every
  node it can reach.  `NodePool.live` exactly tracks live nodes, which
  the tests compare against the reachable-node count to prove old
  versions are released.
- Cancellation is cooperative via `CancelToken.check()` and raises
  `QueryCancelled`; the query's snapshot is released on the way out.

## Running

```
python3.11 -m unittest discover -v
```

See `RESULTS.md` for the recorded output of the actual run.
