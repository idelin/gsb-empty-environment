# polygon-adjacency

Exact shared-edge adjacency between two polygon boundaries.
Python 3.11, standard library only (`fractions`, `dataclasses`, `math`),
tests with `unittest`.

## Core guarantees

- **Exact topology, no epsilon.** Coordinates are `int` or
  `fractions.Fraction`. Collinearity, overlap and incidence are decided by
  exact rational arithmetic (canonical line form `a*x + b*y = c` with
  normalized `Fraction` coefficients). Floats are rejected as input.
- **Shared edge vs point contact.** A boundary overlap of positive length
  is a shared edge; an isolated common point (corner touch, collinear
  endpoint touch, T-junction, crossing) is classified separately as a
  point contact. Sharing only a point is never edge adjacency.
- **Correct aggregation.** Overlaps are merged as interval unions per
  supporting line, so one long edge against two short edges yields a
  single maximal shared edge with the summed length, and duplicated edges
  (a boundary chain tracing the same segment repeatedly) are never
  double counted.
- **Explicit, reported snapping.** Tolerance exists only as an opt-in
  pre-pass (`snap_tolerance`): each vertex of polygon B within exact
  Euclidean distance `<= tolerance` of a vertex of reference polygon A is
  moved onto it. Every displacement is returned as a `SnapRecord`
  (original, snapped position, displacement vector, squared distance).
  Each vertex moves at most once and each target is used at most once, so
  snapping cannot cascade; vertices outside tolerance are untouched and no
  other topology is reinterpreted. After the snap everything is exact
  again.

## Degenerate cases (defined behaviour)

- Zero-length edges (consecutive duplicate vertices) are dropped; they
  create neither shared edges nor point contacts.
- Polygons with fewer than 2 vertices have an empty boundary.
- Input need not be a simple polygon; the closed boundary chain is
  analysed as given. Self-interaction within one polygon is not analysed.
- Touching collinear overlap intervals (`[0,5]` + `[5,10]`) merge into one
  shared edge, because the shared boundary is contiguous.
- Contact points lying on a shared edge (including its endpoints) are not
  additionally reported as point contacts.
- Diagonal shared edges have generally irrational length: the exact value
  is reported as `length_squared` (`Fraction`), plus a float `length`.

## Usage

```python
from fractions import Fraction as F
from polygon_adjacency import analyze

a = [(0, 0), (4, 0), (4, 3), (0, 3)]
b = [(F(401, 100), F(1, 100)), (F(901, 100), F(1, 100)),
     (F(901, 100), F(301, 100)), (F(401, 100), F(301, 100))]

report = analyze(a, b, snap_tolerance=F(1, 10))
print(report.summary())
```

`AdjacencyReport` fields: `shared_edges` (list of `SharedEdge` with exact
endpoints and `length_squared`), `total_shared_length`,
`point_contacts` (sorted exact points), `snap_records`.

## Run

```sh
python3.11 demo.py        # worked examples
python3.11 -m unittest    # test suite (20 tests)
```
