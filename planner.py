"""Grid-based path planning among polygonal obstacles with exact
robot-radius clearance.

Design notes
------------
* All collision / clearance decisions are exact rational comparisons
  (see ``geom.py``). Floats are used *only* for A* path costs and the
  heuristic, which affect path quality, never feasibility.
* The search graph is a grid over the warehouse ``bounds``: axis
  coordinates are ``min + k*step`` plus the exact start/goal
  coordinates, so rational starts/goals are native grid nodes.
  8-connected moves; every candidate edge is validated by exact
  segment-to-polygon clearance, which is what forbids corner cutting:
  a diagonal move whose segment comes closer than ``radius`` to any
  obstacle (or enters it) is rejected outright.
* A robot with ``radius > 0`` cannot squeeze through a point-contact
  gap between two diagonally touching obstacles, because the segment
  clearance there is exactly 0 < r**2. A point robot (``radius == 0``)
  may touch boundaries/vertices -- that is the documented convention.
* If the start (or goal) is covered by an inflated obstacle -- i.e.
  its exact clearance is < radius, or it lies strictly inside an
  obstacle -- planning fails immediately with ``NoPathError``.
* Path simplification (greedy shortcutting) re-validates every
  candidate shortcut segment with the same exact clearance check, so
  the simplified path is guaranteed as safe as the raw grid path.
"""

from __future__ import annotations

import heapq
import math
from dataclasses import dataclass
from fractions import Fraction

from geom import (
    point_in_polygon,
    point_polygon_dist2,
    seg_polygon_dist2,
    segment_enters_interior,
    to_frac,
    to_point,
    validate_polygon,
)


class NoPathError(Exception):
    """Raised when no feasible path exists; the message gives the reason."""


@dataclass(frozen=True)
class Bounds:
    """Axis-aligned warehouse rectangle (exact rationals)."""

    min_x: Fraction
    min_y: Fraction
    max_x: Fraction
    max_y: Fraction

    @staticmethod
    def of(min_x, min_y, max_x, max_y):
        b = Bounds(to_frac(min_x), to_frac(min_y), to_frac(max_x), to_frac(max_y))
        if not (b.min_x < b.max_x and b.min_y < b.max_y):
            raise ValueError("degenerate bounds rectangle")
        return b

    def eroded_contains(self, p, r):
        """True iff the robot *center* may sit at p (bounds eroded by r)."""
        return (
            self.min_x + r <= p[0] <= self.max_x - r
            and self.min_y + r <= p[1] <= self.max_y - r
        )


@dataclass
class Plan:
    waypoints: list  # simplified path (list of exact points)
    grid_path: list  # raw A* grid path before simplification
    radius: Fraction


class GridPlanner:
    def __init__(self, obstacles, radius, bounds, step=1):
        self.radius = to_frac(radius)
        if self.radius < 0:
            raise ValueError("radius must be >= 0")
        self.step = to_frac(step)
        if self.step <= 0:
            raise ValueError("step must be > 0")
        if isinstance(bounds, Bounds):
            self.bounds = bounds
        else:
            self.bounds = Bounds.of(*bounds)
        self.obstacles = []
        for poly in obstacles:
            q = [to_point(v) for v in poly]
            validate_polygon(q)
            self.obstacles.append(q)
        self.r2 = self.radius * self.radius
        self._node_cache = {}
        self._edge_cache = {}

    # ---- exact validity predicates -------------------------------------

    def point_clearance2(self, p):
        """Exact squared clearance of a point to the nearest obstacle,
        or None when there are no obstacles."""
        best = None
        for poly in self.obstacles:
            d = point_polygon_dist2(p, poly)
            if best is None or d < best:
                best = d
        return best

    def node_valid(self, p):
        ok = self._node_cache.get(p)
        if ok is None:
            ok = self.bounds.eroded_contains(p, self.radius) and all(
                not point_in_polygon(p, poly)
                and point_polygon_dist2(p, poly) >= self.r2
                for poly in self.obstacles
            )
            self._node_cache[p] = ok
        return ok

    def edge_valid(self, p, q):
        if p == q:
            return self.node_valid(p)
        key = (p, q) if p <= q else (q, p)
        ok = self._edge_cache.get(key)
        if ok is None:
            ok = (
                self.node_valid(p)
                and self.node_valid(q)
                # endpoints inside the (convex) eroded bounds => segment inside
                and all(
                    not segment_enters_interior(p, q, poly)
                    and seg_polygon_dist2(p, q, poly) >= self.r2
                    for poly in self.obstacles
                )
            )
            self._edge_cache[key] = ok
        return ok

    # ---- planning ------------------------------------------------------

    def plan(self, start, goal):
        start = to_point(start)
        goal = to_point(goal)
        if not self.node_valid(start):
            raise NoPathError(
                "start is covered by an inflated obstacle or outside the "
                "eroded bounds (clearance < radius)"
            )
        if not self.node_valid(goal):
            raise NoPathError(
                "goal is covered by an inflated obstacle or outside the "
                "eroded bounds (clearance < radius)"
            )
        if start == goal:
            return Plan([start], [start], self.radius)
        xs = self._axis(self.bounds.min_x, self.bounds.max_x, {start[0], goal[0]})
        ys = self._axis(self.bounds.min_y, self.bounds.max_y, {start[1], goal[1]})
        grid_path = self._astar(start, goal, xs, ys)
        waypoints = self._simplify(grid_path)
        return Plan(waypoints, grid_path, self.radius)

    def _axis(self, lo, hi, extra):
        coords = set(extra)
        v = lo
        while v <= hi:
            coords.add(v)
            v += self.step
        return sorted(coords)

    def _astar(self, start, goal, xs, ys):
        xi = {x: i for i, x in enumerate(xs)}
        yi = {y: i for i, y in enumerate(ys)}

        def heuristic(p):
            return math.hypot(float(p[0] - goal[0]), float(p[1] - goal[1]))

        def neighbors(p):
            i, j = xi[p[0]], yi[p[1]]
            for di in (-1, 0, 1):
                for dj in (-1, 0, 1):
                    if di == 0 and dj == 0:
                        continue
                    ni, nj = i + di, j + dj
                    if 0 <= ni < len(xs) and 0 <= nj < len(ys):
                        q = (xs[ni], ys[nj])
                        if self.edge_valid(p, q):
                            yield q, math.hypot(
                                float(q[0] - p[0]), float(q[1] - p[1])
                            )

        gscore = {start: 0.0}
        came = {}
        pq = [(heuristic(start), 0.0, start)]
        while pq:
            _, gc, u = heapq.heappop(pq)
            if u == goal:
                path = [u]
                while u in came:
                    u = came[u]
                    path.append(u)
                return path[::-1]
            if gc > gscore.get(u, math.inf) + 1e-9:
                continue  # stale heap entry (float bookkeeping only)
            for v, w in neighbors(u):
                ng = gc + w
                if ng < gscore.get(v, math.inf) - 1e-9:
                    gscore[v] = ng
                    came[v] = u
                    heapq.heappush(pq, (ng + heuristic(v), ng, v))
        raise NoPathError("no collision-free path exists within the bounds")

    def _simplify(self, path):
        """Greedy shortcutting; every shortcut segment is re-validated
        with the exact clearance check before being accepted."""
        if len(path) <= 2:
            return list(path)
        out = [path[0]]
        i = 0
        while i < len(path) - 1:
            j = len(path) - 1
            while j > i + 1 and not self.edge_valid(path[i], path[j]):
                j -= 1
            out.append(path[j])
            i = j
        return out
