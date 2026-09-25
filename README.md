# Exact-Clearance Grid Path Planning among Polygon Obstacles

Python 3.11, standard library only (`fractions`, `heapq`, `math`, `unittest`).

## What it does

* Plans a path for a disk robot of radius `r` inside a rectangular
  warehouse (`Bounds`) among polygonal obstacles.
* Coordinates are integers or rationals (`int` / `str` / `Fraction`;
  `float` is rejected so binary rounding can never affect topology).
* Outputs the simplified path plus an independent per-segment collision
  report (exact squared clearance to every obstacle vs. `r^2`).

## Exactness policy (no epsilon patches)

All topological predicates — orientation, segment intersection,
point-in-polygon, clearance comparisons — are exact rational
computations in `geom.py`. There is **no epsilon anywhere**:

* Clearance rule: a configuration is valid iff `clearance^2 >= r^2`
  (exact `>=`). Contact with an *inflated* obstacle is therefore allowed
  exactly when clearance equals the radius; contact with the obstacle
  itself is allowed only for `r == 0` (point robot).
* Floats are used only for A* cost/heuristic bookkeeping, which affects
  path quality, never feasibility.

## How the requirements are met

* **Radius clearance**: vertices/edges are accepted only if the exact
  squared distance to every obstacle is `>= r^2` (equivalent to
  Minkowski inflation by the disk, without approximating arcs).
* **No corner cutting**: every grid move and every simplified segment
  is validated by exact segment-to-polygon distance; a diagonal move
  that clips a corner closer than `r` (or enters the polygon) is
  rejected — no heuristic neighbor rules are needed for correctness.
* **Diagonal pinch**: two obstacles touching at one point leave a gap
  of zero width; any segment through it has clearance `0 < r^2`, so a
  robot with `r > 0` cannot pass (test
  `test_diagonal_pinch_blocks_robot_with_radius`). A point robot
  (`r = 0`) may pass through the contact point — documented convention.
* **Start/goal covered by inflated obstacle**: planning raises
  `NoPathError` immediately with an explicit reason.
* **Simplification re-checks clearance**: greedy shortcutting accepts a
  shortcut only after the same exact segment validation; `validate.py`
  then re-checks every output segment independently.

## Degenerate cases (defined behavior)

* Polygon with `< 3` vertices, a zero-length edge (duplicate
  consecutive vertices), zero signed area (collinear), or
  self-intersection/self-touching -> `ValueError` from
  `geom.validate_polygon`.
* `radius < 0` or `step <= 0` -> `ValueError`.
* `float` coordinates -> `TypeError` (use `int`, `"1/2"` or `Fraction`).
* `start == goal` -> single-point path, if that point has clearance.
* Start/goal with clearance `< r` or strictly inside an obstacle ->
  `NoPathError`.
* Strict interior is always forbidden, even for `r = 0`
  (`segment_enters_interior` decides this exactly by sampling midpoints
  between exact boundary-crossing parameters).
* Degenerate zero-length segments are handled as points in all
  distance/intersection routines.

## Limitations (by design)

* Search is a grid with spacing `step` (rational) over the warehouse
  bounds; start/goal coordinates are injected as exact grid lines, so
  rational endpoints are native nodes. A path that needs corridors
  narrower than `step` alignment may require a smaller `step`.
* The robot must stay inside `bounds` eroded by `r`.

## Files

* `geom.py` — exact rational geometry kernel.
* `planner.py` — `GridPlanner` (grid A* + exact validation + simplification), `Bounds`, `NoPathError`.
* `validate.py` — independent per-segment collision/clearance reports.
* `main.py` — runnable demo scenarios.
* `test_planner.py` — 25 unittest cases.

## Run

```sh
python3.11 main.py                 # demo scenarios with per-segment reports
python3.11 -m unittest test_planner -v   # test suite
```

## Test result

```
Ran 25 tests in 2.579s

OK
```
