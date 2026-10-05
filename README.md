# rtree

R-tree style hierarchical bounding-box index over 2D signed-integer
rectangles. Rectangles may degenerate into lines or points; intersection is
closed-interval, so touching boundaries count as intersecting.

## API

- `New(capacity)` — node capacity is a parameter (min 4); min fill is
  `ceil(capacity/2)`.
- `Insert(id, rect)` / `Delete(id)` / `Replace(id, rect)` — IDs are unique;
  `Replace` is atomic (unknown ID ⇒ `ErrNotFound`, tree untouched).
- `RangeQuery(rect)` — returns IDs sorted ascending plus the number of
  visited nodes (pruning metric). Never scans all objects.
- `Validate()` — independent structural audit: parent/child references,
  level heights, capacity bounds, tight child MBRs, object uniqueness and
  index-map consistency.

## Structural rules

- **Split**: quadratic (Guttman) — seed with the max dead-space pair,
  assign by strongest preference / least enlargement, min fill guaranteed.
- **Underflow**: nodes below min fill are detached and their entries
  re-inserted at their original level (objects at level 0, internal
  children at their own level). No ghost objects after delete.
- **Root**: overflow creates a new root one level up; a single-child root
  is collapsed (repeatedly); an emptied internal root becomes an empty leaf.
- Every internal entry rectangle is always the tight MBR of its child.

## Tests

```
go test ./...
```

Covers random mutations on a small integer grid cross-checked against a
full scan, boundary touching, all-identical rectangles, repeated root
split/shrink cycles, replace-failure rollback, validator corruption
detection, and visited-node pruning counts.

## CLI

```
go run ./cmd/rtreecli -capacity 8
```

Line-delimited JSON in, JSON out. Ops: `insert`, `replace`, `delete`,
`query` (returns `ids` and `visited`), `validate`, `size`, `height`.

```
{"op":"insert","id":1,"rect":[0,0,5,5]}
{"op":"query","rect":[5,5,9,9]}
=> {"ids":[1],"ok":true,"visited":3}
```
