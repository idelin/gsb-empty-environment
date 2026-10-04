# edtgrid — exact squared Euclidean distance transform for binary masks

For every pixel of a finite 2D integer grid, `edtgrid` computes the squared
Euclidean distance to the **nearest opposite-valued pixel** together with
that pixel's coordinates. Ties are broken by row, then column. If the whole
grid holds a single value, every pixel reports the no-target marker
(`dist = -1`, `hasTarget = false`).

Rectangular region **flips** produce a new immutable snapshot and report the
exact set of pixels whose distance or nearest point changed. Old snapshots
keep answering queries with their old distances.

## Layout

- `grid.go` — core engine: immutable `Grid`, `Flip`, exact separable EDT.
- `reference.go` — all-pairs O(N²) reference (no incremental index), used as
  ground truth in tests.
- `cli/` — line-delimited JSON command interface.
- `cmd/edtcli` — CLI binary (stdin/stdout).
- `grid_test.go`, `cli/cli_test.go` — tests (see below).

## JSON CLI

One JSON request per line on stdin, one JSON response per line on stdout.

```
{"cmd":"new","rows":5,"cols":5,"fill":0}          -> {"ok":true,"version":0,...}
{"cmd":"set","row":2,"col":2,"value":1}           -> {"ok":true,"version":1,"changed":[...],"examinedPixels":25,...}
{"cmd":"flip","row0":1,"col0":1,"row1":3,"col1":3}   half-open rectangle
{"cmd":"query","row":0,"col":0,"version":1}       -> {"ok":true,"hasTarget":true,"dist":8,"source":{"row":2,"col":2},...}
{"cmd":"query","row":0,"col":0,"version":0}       -> {"ok":true,"hasTarget":false,"dist":-1,...}
{"cmd":"dump","version":1}                        -> all cells of that snapshot
```

Every edit creates a new version; `version` is optional in queries and
defaults to the latest snapshot. Flip responses include:

- `changed`: exact list of pixels whose distance or nearest point changed
  (row-then-column sorted, **not** clipped to the edit box);
- `examinedPixels`: how many grid pixels the update inspected;
- `fullRecompute`: whether the update degenerated to a full recomputation;
- `noOp`: true when the flip changed no mask value (empty/out-of-range rect).

## Correctness strategy

The engine uses the exact separable (Felzenszwalb–Huttenlocher) squared
distance transform: one 1D lower-envelope pass per column, then one per row,
run twice (nearest 0-pixel and nearest 1-pixel). The 1D envelope thresholds
are computed in exact integer arithmetic and embed the tie-break rule, so
among equidistant sources the winner is always the row-then-column smallest
one. The transform is exact — no approximation, no chamfer metric.

The reference implementation (`ReferenceEDT`) keeps no index of any kind and
simply scans all pixel pairs per query pixel. Tests validate the engine
against it by **exhaustively enumerating** every mask on 2x2, 2x3, 3x2 and
3x3 grids together with every rectangular flip (including the reported
change sets), plus randomized flip sequences on larger grids, and targeted
cases: single pixel, checkerboard, two equidistant seeds, and deleting the
unique opposite-valued pixel (which correctly flips the whole image to the
no-target marker).

## Complexity

Let `N = rows * cols`.

| Operation            | Time        | Extra space | Notes |
|----------------------|-------------|-------------|-------|
| Build transform      | O(N)        | O(N)        | two separable passes, twice (both values) |
| Point query          | O(1)        | —           | array lookup on an immutable snapshot |
| Flip update          | O(N)        | O(N)        | currently always a full recomputation |
| Change-set diff      | O(N)        | O(N)        | exact pixel-wise comparison of snapshots |
| Snapshot retention   | —           | O(N) each   | old versions stay queryable |
| Reference (tests)    | O(N²)       | O(N)        | all pixel pairs, no index |

**Why full recomputation on flips.** A flip deletes existing nearest points
(a flipped pixel stops being a valid target for its former dependents), and
deleting the unique nearest point can move nearest points arbitrarily far
away — the change region is not local to the edit box. Rather than
maintaining a fragile incremental index, the update degenerates to the exact
O(N) recomputation and honestly reports `examinedPixels = N` (and `0` for
no-op flips). The change set is computed exactly by diffing the two
immutable snapshots, so consumers always get the precise, globally correct
delta regardless of where it lands.

Out of scope by design: 3D isosurfaces and hard real-time latency bounds.

## Run

```
GO111MODULE=on go test ./...
GO111MODULE=on go build ./cmd/edtcli
echo '{"cmd":"new","rows":5,"cols":5,"fill":0}' | ./edtcli
```
