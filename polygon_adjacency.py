"""Exact polygon-boundary adjacency analysis.

Determines shared-edge adjacency between two polygon boundaries, the exact
shared lengths, and isolated point contacts (classified separately).

Design rules
------------
* Coordinates are integers or ``fractions.Fraction``.  All topological
  decisions (collinearity, overlap, incidence) are made with exact rational
  arithmetic.  No epsilon is ever used to decide topology.
* The only place a tolerance appears is the *optional, explicit* vertex
  snapping pre-pass requested by the caller.  Every snap is reported with
  its displacement, and snapping only merges near-coincident vertex pairs;
  it never reinterprets any other topology.
* Sharing a single point is NOT edge adjacency; it is a point contact.
* Overlapping contributions are merged as interval unions per supporting
  line, so one long edge against two short edges aggregates correctly and
  duplicated edges are never double counted.

Degenerate cases (defined behaviour)
------------------------------------
* Zero-length edges (consecutive duplicate vertices) are dropped before
  analysis; they create neither shared edges nor point contacts.
* Polygons with fewer than two vertices have an empty boundary.
* Input need not be a simple polygon: the algorithm operates on the closed
  boundary chain as given.  Self-interaction inside one polygon is not
  analysed; only A-vs-B contacts are reported.
* Touching collinear overlap intervals (e.g. [0,5] and [5,10]) merge into
  one shared edge, because the shared boundary is contiguous.
* Shared-edge lengths of diagonal edges are generally irrational; the exact
  value is reported as ``length_squared`` (a ``Fraction``) together with a
  float ``length`` approximation.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Sequence

Point = tuple[Fraction, Fraction]


# ---------------------------------------------------------------------------
# exact primitives
# ---------------------------------------------------------------------------

def to_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        f"coordinates must be int or fractions.Fraction, got {type(value).__name__}"
    )


def to_point(pair) -> Point:
    x, y = pair
    return (to_fraction(x), to_fraction(y))


def normalize_polygon(vertices: Sequence) -> list[Point]:
    return [to_point(v) for v in vertices]


def _sub(a: Point, b: Point) -> Point:
    return (a[0] - b[0], a[1] - b[1])


def _cross(a: Point, b: Point) -> Fraction:
    return a[0] * b[1] - a[1] * b[0]


def _dist2(a: Point, b: Point) -> Fraction:
    return (a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2


def _edges(vertices: Sequence[Point]) -> list[tuple[Point, Point]]:
    """Closed boundary chain as segments; zero-length edges dropped."""
    n = len(vertices)
    result = []
    for i in range(n):
        p = vertices[i]
        q = vertices[(i + 1) % n]
        if p != q:
            result.append((p, q))
    return result


class _Line:
    """Canonical supporting line a*x + b*y = c with exact Fractions."""

    __slots__ = ("a", "b", "c", "use_x", "scale_sq")

    def __init__(self, p: Point, q: Point):
        a = p[1] - q[1]
        b = q[0] - p[0]
        c = a * p[0] + b * p[1]
        n = a if a != 0 else b
        self.a = a / n
        self.b = b / n
        self.c = c / n
        # Parameter axis: the dominant direction component, so the choice is
        # a pure function of the canonical line (consistent for all segments
        # on the same line).
        self.use_x = abs(self.b) >= abs(self.a)
        denom = self.b ** 2 if self.use_x else self.a ** 2
        # (dlength / dparam)^2, exact.
        self.scale_sq = (self.a ** 2 + self.b ** 2) / denom

    def key(self) -> tuple[Fraction, Fraction, Fraction]:
        return (self.a, self.b, self.c)

    def contains(self, pt: Point) -> bool:
        return self.a * pt[0] + self.b * pt[1] == self.c

    def param(self, pt: Point) -> Fraction:
        return pt[0] if self.use_x else pt[1]

    def point_at(self, t: Fraction) -> Point:
        if self.use_x:
            return (t, (self.c - self.a * t) / self.b)
        return ((self.c - self.b * t) / self.a, t)


def _on_segment(p: Point, q: Point, pt: Point) -> bool:
    return (
        min(p[0], q[0]) <= pt[0] <= max(p[0], q[0])
        and min(p[1], q[1]) <= pt[1] <= max(p[1], q[1])
    )


def _segment_intersection_point(p1, q1, p2, q2):
    """Exact intersection point of two non-parallel segments, or None."""
    d1 = _sub(q1, p1)
    d2 = _sub(q2, p2)
    denom = _cross(d1, d2)
    if denom == 0:
        return None
    w = _sub(p2, p1)
    t = _cross(w, d2) / denom
    pt = (p1[0] + t * d1[0], p1[1] + t * d1[1])
    if _on_segment(p1, q1, pt) and _on_segment(p2, q2, pt):
        return pt
    return None


def _merge_intervals(intervals):
    """Union of closed intervals; touching intervals merge."""
    merged: list[list[Fraction]] = []
    for lo, hi in sorted(intervals):
        if merged and lo <= merged[-1][1]:
            if hi > merged[-1][1]:
                merged[-1][1] = hi
        else:
            merged.append([lo, hi])
    return [(lo, hi) for lo, hi in merged]


# ---------------------------------------------------------------------------
# public result types
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class SharedEdge:
    """One maximal contiguous piece of shared boundary."""

    start: Point
    end: Point
    length_squared: Fraction
    length: float


@dataclass(frozen=True)
class SnapRecord:
    """One vertex displacement applied by the snapping pre-pass."""

    polygon: str  # always "B": polygon B is snapped onto reference polygon A
    vertex_index: int
    original: Point
    snapped: Point
    displacement: Point  # vector actually applied
    distance_squared: Fraction


@dataclass
class AdjacencyReport:
    shared_edges: list[SharedEdge] = field(default_factory=list)
    point_contacts: list[Point] = field(default_factory=list)
    snap_records: list[SnapRecord] = field(default_factory=list)
    total_shared_length: float = 0.0

    def summary(self) -> str:
        lines = []
        lines.append(f"shared edges: {len(self.shared_edges)}"
                     f" (total length {self.total_shared_length!r})")
        for e in self.shared_edges:
            lines.append(
                f"  {e.start} -> {e.end}"
                f"  length^2={e.length_squared} length={e.length!r}"
            )
        lines.append(f"point contacts: {len(self.point_contacts)}")
        for p in self.point_contacts:
            lines.append(f"  {p}")
        lines.append(f"snap displacements: {len(self.snap_records)}")
        for r in self.snap_records:
            lines.append(
                f"  {r.polygon}[{r.vertex_index}] {r.original} -> {r.snapped}"
                f"  displacement={r.displacement} |d|^2={r.distance_squared}"
            )
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# snapping (optional, explicit, reported)
# ---------------------------------------------------------------------------

def snap_vertices(
    reference: Sequence[Point],
    moving: Sequence[Point],
    tolerance,
) -> tuple[list[Point], list[SnapRecord]]:
    """Snap each vertex of ``moving`` onto the nearest vertex of
    ``reference`` whose exact Euclidean distance is <= ``tolerance``.

    * Greedy nearest-first matching; every reference vertex is used as a
      snap target at most once and every moving vertex moves at most once,
      so snapping cannot cascade into unrelated topology.
    * Vertices with no reference vertex within tolerance are untouched.
    * Every applied displacement is returned as a ``SnapRecord``.
    """
    tol = to_fraction(tolerance)
    if tol < 0:
        raise ValueError("snap tolerance must be >= 0")
    tol2 = tol * tol
    used_targets: set[int] = set()
    snapped = list(moving)
    records: list[SnapRecord] = []
    for j, pt in enumerate(moving):
        best = None
        for i, rp in enumerate(reference):
            if i in used_targets:
                continue
            d2 = _dist2(pt, rp)
            if d2 <= tol2 and (best is None or d2 < best[0]):
                best = (d2, i)
        if best is None:
            continue
        d2, i = best
        used_targets.add(i)
        if d2 == 0:
            continue  # already coincident: nothing to report
        target = reference[i]
        snapped[j] = target
        records.append(
            SnapRecord(
                polygon="B",
                vertex_index=j,
                original=pt,
                snapped=target,
                displacement=_sub(target, pt),
                distance_squared=d2,
            )
        )
    return snapped, records


# ---------------------------------------------------------------------------
# main analysis
# ---------------------------------------------------------------------------

def analyze(
    polygon_a: Sequence,
    polygon_b: Sequence,
    snap_tolerance=None,
) -> AdjacencyReport:
    """Analyse shared-edge adjacency and point contacts between two polygons.

    ``polygon_a`` / ``polygon_b`` are sequences of ``(x, y)`` pairs with
    ``int`` or ``Fraction`` coordinates, implicitly closed.

    If ``snap_tolerance`` (int or Fraction) is given, polygon B's vertices
    are first snapped onto polygon A's vertices (see ``snap_vertices``);
    all displacements appear in ``report.snap_records``.  Everything after
    the snap is exact.
    """
    verts_a = normalize_polygon(polygon_a)
    verts_b = normalize_polygon(polygon_b)

    snap_records: list[SnapRecord] = []
    if snap_tolerance is not None:
        verts_b, snap_records = snap_vertices(verts_a, verts_b, snap_tolerance)

    edges_a = _edges(verts_a)
    edges_b = _edges(verts_b)

    lines: dict[tuple, _Line] = {}
    overlap_intervals: dict[tuple, list[tuple[Fraction, Fraction]]] = {}
    candidate_points: set[Point] = set()

    def line_of(p: Point, q: Point) -> _Line:
        line = _Line(p, q)
        return lines.setdefault(line.key(), line)

    for p1, q1 in edges_a:
        line1 = line_of(p1, q1)
        lo1, hi1 = sorted((line1.param(p1), line1.param(q1)))
        for p2, q2 in edges_b:
            line2 = line_of(p2, q2)
            if line1.key() == line2.key():
                lo2, hi2 = sorted((line2.param(p2), line2.param(q2)))
                lo, hi = max(lo1, lo2), min(hi1, hi2)
                if lo < hi:
                    overlap_intervals.setdefault(line1.key(), []).append((lo, hi))
                elif lo == hi:
                    candidate_points.add(line1.point_at(lo))
            else:
                pt = _segment_intersection_point(p1, q1, p2, q2)
                if pt is not None:
                    candidate_points.add(pt)

    report = AdjacencyReport(snap_records=snap_records)

    shared_intervals: list[tuple[_Line, Fraction, Fraction]] = []
    for key, intervals in overlap_intervals.items():
        line = lines[key]
        for lo, hi in _merge_intervals(intervals):
            shared_intervals.append((line, lo, hi))
            start, end = line.point_at(lo), line.point_at(hi)
            length_sq = (hi - lo) ** 2 * line.scale_sq
            report.shared_edges.append(
                SharedEdge(
                    start=start,
                    end=end,
                    length_squared=length_sq,
                    length=math.sqrt(float(length_sq)),
                )
            )

    report.shared_edges.sort(key=lambda e: (e.start, e.end))
    report.total_shared_length = sum(e.length for e in report.shared_edges)

    def covered_by_shared_edge(pt: Point) -> bool:
        for line, lo, hi in shared_intervals:
            if line.contains(pt) and lo <= line.param(pt) <= hi:
                return True
        return False

    report.point_contacts = sorted(
        pt for pt in candidate_points if not covered_by_shared_edge(pt)
    )
    return report
