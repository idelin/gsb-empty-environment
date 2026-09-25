"""Grid path planning among polygonal obstacles with exact radius clearance.

Model
-----
* The robot is a closed disk of radius ``radius`` (an exact rational).
* A configuration is **free iff dist(point, obstacle) >= radius** for every
  obstacle; touching (distance exactly == radius) is allowed.  For a
  zero-radius robot the convention is tightened to ``dist > 0`` so a point
  robot can never occupy the obstacle itself.  Both rules are evaluated
  exactly on rational numbers -- no epsilon is involved anywhere.
* The free space is discretised into square cells of ``cell_size``.  A cell
  is **blocked iff its closed square comes within < radius of an obstacle**
  (equivalently: intersects the *open* Minkowski dilation of the obstacle by
  the robot disk).  This makes the grid conservative: any centre path that
  stays inside free cells automatically keeps the required clearance.
* Connectivity is 8-neighbourhood with a strict **no-corner-cutting** rule:
  a diagonal step is allowed only when both orthogonal neighbours are free,
  so the robot can never squeeze diagonally past an inflated obstacle corner
  (e.g. through the gap where two diagonal obstacles nearly touch).
* After the grid search, the path is greedily simplified with straight-line
  shortcuts, but every candidate shortcut is re-validated for exact
  clearance first, and every segment of the final path is validated again.

Failure modes (all exact):
* start or goal covered by an inflated obstacle  -> ``plan`` returns None;
* start/goal outside ``bounds`` (when given)     -> None;
* no route on the grid at this resolution        -> None;
* final validation of any segment fails          -> None.
"""

from __future__ import annotations

import heapq
import math
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple

from geometry import as_point, dist2_cell_shape, dist2_point_shape, dist2_seg_shape

_ORTHO = ((1, 0), (-1, 0), (0, 1), (0, -1))
_DIAG = ((1, 1), (1, -1), (-1, 1), (-1, -1))
_SQRT2 = math.sqrt(2.0)

Point = Tuple[Fraction, Fraction]


@dataclass
class SegmentCheck:
    """Collision-validation record for one path segment."""

    index: int
    start: Point
    end: Point
    min_dist2: Optional[Fraction]   # exact squared clearance to nearest obstacle
    required_dist2: Fraction        # radius**2
    clear: bool

    @property
    def min_dist(self) -> Optional[float]:
        """Approximate (display-only) clearance distance."""
        return None if self.min_dist2 is None else math.sqrt(float(self.min_dist2))


@dataclass
class PlanResult:
    path: List[Point]            # simplified waypoints (exact rationals)
    raw_path: List[Point]        # grid waypoints before simplification
    segments: List[SegmentCheck]  # per-segment collision validation

    @property
    def ok(self) -> bool:
        return all(s.clear for s in self.segments)


class GridPlanner:
    """Plans clearance-respecting paths on an exact rational grid."""

    def __init__(self, obstacles: Sequence[Sequence[Sequence]],
                 radius, cell_size=1,
                 bounds: Optional[Sequence] = None, margin_cells: int = 2):
        self.obstacles = [[as_point(p) for p in poly] for poly in obstacles]
        for poly in self.obstacles:
            if not poly:
                raise ValueError("obstacle polygon must have >= 1 vertex")
        self.radius = Fraction(radius)
        if self.radius < 0:
            raise ValueError("radius must be >= 0")
        self.r2 = self.radius ** 2
        self.cell = Fraction(cell_size)
        if self.cell <= 0:
            raise ValueError("cell_size must be > 0")
        self.bounds = None if bounds is None else tuple(Fraction(b) for b in bounds)
        self.margin_cells = int(margin_cells)
        # Grid state, (re)built per plan() call.
        self.origin: Point = (Fraction(0), Fraction(0))
        self.ni = self.nj = 0
        self.blocked: List[List[bool]] = []
        self._forced_free = set()

    # ------------------------------------------------------------------
    # Exact clearance predicates
    # ------------------------------------------------------------------
    def _is_clear(self, d2: Fraction) -> bool:
        """The single exact clearance rule used everywhere."""
        if d2 < self.r2:
            return False
        if self.r2 == 0 and d2 == 0:
            return False
        return True

    def point_clear(self, p) -> bool:
        """True iff a robot centred at p keeps the required clearance."""
        p = as_point(p)
        return all(self._is_clear(dist2_point_shape(p, poly))
                   for poly in self.obstacles)

    def segment_clear(self, p, q) -> bool:
        """True iff sweeping the robot centre along pq keeps clearance."""
        p, q = as_point(p), as_point(q)
        if self.bounds is not None and not (self._in_bounds(p) and self._in_bounds(q)):
            return False
        return all(self._is_clear(dist2_seg_shape(p, q, poly))
                   for poly in self.obstacles)

    def _in_bounds(self, p: Point) -> bool:
        x0, y0, x1, y1 = self.bounds
        return x0 <= p[0] <= x1 and y0 <= p[1] <= y1

    # ------------------------------------------------------------------
    # Grid construction
    # ------------------------------------------------------------------
    def _build_grid(self, start: Point, goal: Point) -> None:
        if self.bounds is not None:
            x0, y0, x1, y1 = self.bounds
            i0, j0 = math.floor(x0 / self.cell), math.floor(y0 / self.cell)
            i1, j1 = math.ceil(x1 / self.cell), math.ceil(y1 / self.cell)
        else:
            xs = [p[0] for poly in self.obstacles for p in poly] + [start[0], goal[0]]
            ys = [p[1] for poly in self.obstacles for p in poly] + [start[1], goal[1]]
            m = self.radius + self.margin_cells * self.cell
            i0 = math.floor((min(xs) - m) / self.cell)
            j0 = math.floor((min(ys) - m) / self.cell)
            i1 = math.ceil((max(xs) + m) / self.cell)
            j1 = math.ceil((max(ys) + m) / self.cell)
        self.origin = (self.cell * i0, self.cell * j0)
        self.ni, self.nj = i1 - i0, j1 - j0
        self.blocked = [[False] * self.nj for _ in range(self.ni)]
        for i in range(self.ni):
            for j in range(self.nj):
                x0 = self.origin[0] + i * self.cell
                y0 = self.origin[1] + j * self.cell
                x1, y1 = x0 + self.cell, y0 + self.cell
                corners = [(x0, y0), (x1, y0), (x1, y1), (x0, y1)]
                for poly in self.obstacles:
                    if not self._is_clear(dist2_cell_shape(corners, poly)):
                        self.blocked[i][j] = True
                        break

    def _cell_of(self, p: Point) -> Tuple[int, int]:
        i = math.floor((p[0] - self.origin[0]) / self.cell)
        j = math.floor((p[1] - self.origin[1]) / self.cell)
        return (min(max(i, 0), self.ni - 1), min(max(j, 0), self.nj - 1))

    def _center(self, i: int, j: int) -> Point:
        half = self.cell / 2
        return (self.origin[0] + i * self.cell + half,
                self.origin[1] + j * self.cell + half)

    def _free(self, i: int, j: int) -> bool:
        if not (0 <= i < self.ni and 0 <= j < self.nj):
            return False
        if (i, j) in self._forced_free:
            return True
        return not self.blocked[i][j]

    def _neighbors(self, cell: Tuple[int, int]):
        """Free neighbours; diagonal steps require both orthogonal cells free
        (no corner cutting)."""
        i, j = cell
        for di, dj in _ORTHO:
            if self._free(i + di, j + dj):
                yield (i + di, j + dj), 1.0
        for di, dj in _DIAG:
            if not self._free(i + di, j + dj):
                continue
            if not (self._free(i + di, j) and self._free(i, j + dj)):
                continue  # would cut an inflated-obstacle corner
            yield (i + di, j + dj), _SQRT2

    def _dijkstra(self, start_cell, goal_cell) -> Optional[List[Tuple[int, int]]]:
        inf = math.inf
        dist = {start_cell: 0.0}
        prev = {}
        pq = [(0.0, start_cell)]
        while pq:
            d, u = heapq.heappop(pq)
            if d > dist.get(u, inf):
                continue
            if u == goal_cell:
                break
            for v, w in self._neighbors(u):
                nd = d + w
                if nd < dist.get(v, inf):
                    dist[v] = nd
                    prev[v] = u
                    heapq.heappush(pq, (nd, v))
        if goal_cell not in dist:
            return None
        cells = [goal_cell]
        while cells[-1] != start_cell:
            cells.append(prev[cells[-1]])
        cells.reverse()
        return cells

    # ------------------------------------------------------------------
    # Path simplification + validation
    # ------------------------------------------------------------------
    def _simplify(self, pts: List[Point]) -> List[Point]:
        """Greedy shortcutting; every shortcut is clearance-validated first."""
        if len(pts) <= 2:
            return list(pts)
        out = [pts[0]]
        i = 0
        while i < len(pts) - 1:
            j = len(pts) - 1
            while j > i + 1 and not self.segment_clear(pts[i], pts[j]):
                j -= 1
            out.append(pts[j])
            i = j
        return out

    def validate_path(self, path: Sequence[Sequence]) -> List[SegmentCheck]:
        """Exact per-segment collision validation of an arbitrary path."""
        pts = [as_point(p) for p in path]
        checks = []
        for k in range(len(pts) - 1):
            p, q = pts[k], pts[k + 1]
            best = None
            for poly in self.obstacles:
                d2 = dist2_seg_shape(p, q, poly)
                if best is None or d2 < best:
                    best = d2
            clear = best is None or self._is_clear(best)
            if self.bounds is not None and not (self._in_bounds(p) and self._in_bounds(q)):
                clear = False
            checks.append(SegmentCheck(k, p, q, best, self.r2, clear))
        return checks

    # ------------------------------------------------------------------
    # Main entry point
    # ------------------------------------------------------------------
    def plan(self, start, goal) -> Optional[PlanResult]:
        start, goal = as_point(start), as_point(goal)
        if self.bounds is not None and not (self._in_bounds(start)
                                            and self._in_bounds(goal)):
            return None
        # Start/goal covered by an inflated obstacle -> no solution.
        if not self.point_clear(start) or not self.point_clear(goal):
            return None
        if start == goal:
            return PlanResult([start], [start], [])
        self._build_grid(start, goal)
        sc, gc = self._cell_of(start), self._cell_of(goal)
        # The exact points are known-clear; allow their cells even if the
        # (conservative) cell test blocked them.  Final segment validation
        # still guarantees soundness of whatever comes out.
        self._forced_free = {sc, gc}
        cells = self._dijkstra(sc, gc)
        if cells is None:
            return None
        raw = [start] + [self._center(i, j) for i, j in cells[1:-1]] + [goal]
        path = self._simplify(raw)
        segments = self.validate_path(path)
        if not all(s.clear for s in segments):
            return None
        return PlanResult(path, raw, segments)
