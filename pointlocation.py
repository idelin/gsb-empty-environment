"""Exact multi-ring point location (inside / outside / boundary).

Design notes
------------
* Coordinates are exact: ``int`` or ``fractions.Fraction`` only.  Floats are
  rejected so that no epsilon-based patching can sneak in.  All predicates
  (point-on-segment, ray crossing) are evaluated with exact rational
  arithmetic, so degenerate configurations have deterministic, documented
  outcomes instead of being "fixed" with a tolerance.
* Hierarchy is explicit: one outer ring plus an explicit list of hole rings.
  Ring orientation (CW/CCW) is irrelevant and never inspected.
* Semantics:
    - a point exactly on any edge or vertex (outer or hole) -> BOUNDARY
    - a point strictly inside the outer ring and strictly outside every
      hole -> INSIDE
    - everything else -> OUTSIDE (this includes the interior of holes)
* Degenerate cases (defined behaviour):
    - rings are normalised: consecutive duplicate vertices and a repeated
      closing vertex are removed, so ``[A, B, C, A]`` == ``[A, B, C]``;
    - a ring with fewer than 3 distinct vertices has no interior, but its
      vertices/segments still count as boundary;
    - a zero-area (collinear) ring has no interior, its segments are
      boundary;
    - holes are expected to lie inside the outer ring; if they do not, the
      boolean formula above still applies verbatim;
    - self-intersecting rings are classified with the even-odd rule.
* Ray casting uses the half-open rule ``(y1 > py) != (y2 > py)`` with a
  strict ``x_int > px`` comparison, so a ray passing exactly through a
  vertex counts it either once (monotone pass-through) or twice (local
  extremum) -- never the wrong parity.  Horizontal edges are never counted
  as crossings; points lying on them are caught by the boundary test.
* Batch queries are served by two spatial indexes built once:
    - a uniform grid over all edges for boundary candidates;
    - a per-ring slab index (edges sorted by min-y, bisected at query time)
      that restricts ray casting to edges actually straddling the ray.
"""

from __future__ import annotations

from bisect import bisect_right
from enum import Enum
from fractions import Fraction
from math import floor, isqrt
from typing import Iterable, Sequence

__all__ = ["Location", "Locator", "normalize_point", "normalize_ring"]


class Location(Enum):
    INSIDE = "inside"
    OUTSIDE = "outside"
    BOUNDARY = "boundary"

    def __str__(self) -> str:
        return self.value


def _coord(value) -> Fraction:
    if isinstance(value, bool) or not isinstance(value, (int, Fraction)):
        raise TypeError(
            f"coordinate must be int or Fraction, got {type(value).__name__}"
        )
    return Fraction(value)


def normalize_point(point) -> tuple[Fraction, Fraction]:
    """Convert an ``(x, y)`` pair of int/Fraction into exact Fractions."""
    try:
        x, y = point
    except (TypeError, ValueError) as exc:
        raise TypeError(f"point must be an (x, y) pair, got {point!r}") from exc
    return (_coord(x), _coord(y))


def normalize_ring(points: Iterable) -> list[tuple[Fraction, Fraction]]:
    """Normalise a ring: exact coords, no consecutive/closing duplicates."""
    deduped: list[tuple[Fraction, Fraction]] = []
    for raw in points:
        p = normalize_point(raw)
        if not deduped or p != deduped[-1]:
            deduped.append(p)
    while len(deduped) > 1 and deduped[0] == deduped[-1]:
        deduped.pop()
    return deduped


def _ring_edges(ring):
    if len(ring) < 2:
        return []
    return [(ring[i], ring[(i + 1) % len(ring)]) for i in range(len(ring))]


def _point_on_segment(p, a, b) -> bool:
    """Exact collinearity + bounding-box test; True iff p lies on segment ab."""
    cross = (b[0] - a[0]) * (p[1] - a[1]) - (b[1] - a[1]) * (p[0] - a[0])
    if cross != 0:
        return False
    return (
        min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


class _BoundaryGrid:
    """Uniform grid over unique edges; answers boundary-candidate lookups."""

    def __init__(self, edges):
        unique = {}
        for a, b in edges:
            key = (a, b) if a <= b else (b, a)
            unique.setdefault(key, (a, b))
        self._edges = list(unique.values())
        xs = [p[0] for edge in self._edges for p in edge]
        ys = [p[1] for edge in self._edges for p in edge]
        span = max(max(xs) - min(xs), max(ys) - min(ys)) if xs else Fraction(0)
        buckets = max(1, isqrt(len(self._edges)))
        self._cell = span / buckets if span > 0 else Fraction(1)
        self._grid: dict[tuple[int, int], list] = {}
        for edge in self._edges:
            for key in self._cells_of(edge):
                self._grid.setdefault(key, []).append(edge)

    def _cells_of(self, edge):
        (ax, ay), (bx, by) = edge
        x0, x1 = min(ax, bx), max(ax, bx)
        y0, y1 = min(ay, by), max(ay, by)
        for ix in range(floor(x0 / self._cell), floor(x1 / self._cell) + 1):
            for iy in range(floor(y0 / self._cell), floor(y1 / self._cell) + 1):
                yield (ix, iy)

    def candidates(self, p):
        key = (floor(p[0] / self._cell), floor(p[1] / self._cell))
        return self._grid.get(key, ())


class _RayIndex:
    """Slab index: edges sorted by min-y for fast horizontal-ray queries."""

    def __init__(self, edges):
        self._edges = sorted(
            edges, key=lambda e: (min(e[0][1], e[1][1]), max(e[0][1], e[1][1]))
        )
        self._ymins = [min(e[0][1], e[1][1]) for e in self._edges]

    def straddling(self, py) -> list:
        """Edges with ``ymin <= py < ymax`` (the half-open crossing rule)."""
        stop = bisect_right(self._ymins, py)
        return [e for e in self._edges[:stop] if max(e[0][1], e[1][1]) > py]


class Locator:
    """Point locator for one outer ring with an explicit list of holes."""

    def __init__(self, outer: Sequence, holes: Iterable[Sequence] = ()):
        self.outer = normalize_ring(outer)
        self.holes = [normalize_ring(h) for h in holes]
        self._rings = [self.outer, *self.holes]
        all_edges = [e for ring in self._rings for e in _ring_edges(ring)]
        self._boundary = _BoundaryGrid(all_edges)
        # Rings with < 3 vertices have no interior: no ray index at all.
        self._ray = [
            _RayIndex(_ring_edges(ring)) if len(ring) >= 3 else None
            for ring in self._rings
        ]

    def _on_boundary(self, p) -> bool:
        return any(
            _point_on_segment(p, a, b) for a, b in self._boundary.candidates(p)
        )

    def _inside_ring(self, ring_idx: int, p) -> bool:
        index = self._ray[ring_idx]
        if index is None:
            return False
        px, py = p
        crossings = 0
        for a, b in index.straddling(py):
            x_int = a[0] + (py - a[1]) * (b[0] - a[0]) / (b[1] - a[1])
            if x_int > px:
                crossings += 1
        return crossings % 2 == 1

    def locate(self, point) -> Location:
        p = normalize_point(point)
        if self._on_boundary(p):
            return Location.BOUNDARY
        if not self._inside_ring(0, p):
            return Location.OUTSIDE
        for idx in range(1, len(self._rings)):
            if self._inside_ring(idx, p):
                return Location.OUTSIDE
        return Location.INSIDE

    def locate_many(self, points: Iterable) -> list[Location]:
        """Batch query; every lookup is served by the spatial indexes."""
        return [self.locate(p) for p in points]
