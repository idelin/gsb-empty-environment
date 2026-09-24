"""Exact shared-boundary adjacency for polygons with rational coordinates.

All geometry is computed with fractions.Fraction: no floating-point epsilon
is used anywhere in topology decisions. Coordinates may be given as int,
Fraction, or any value Fraction() accepts (including numeric strings).

Degenerate-case definitions (see README.md for the full list):
  * Zero-length edges (consecutive duplicate vertices) are dropped.
  * Collinear consecutive vertices (180-degree vertices) are allowed; results
    are invariant to inserting or removing such vertices.
  * Duplicate / retraced edges contribute their shared length exactly once
    (overlap intervals are merged as a union, never summed per edge pair).
  * A contact consisting of isolated points only is NOT edge adjacency.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd, isqrt, sqrt

Point = tuple[Fraction, Fraction]


def as_point(p) -> Point:
    """Coerce a 2-sequence of int/Fraction/str into an exact rational point."""
    x, y = p
    return (Fraction(x), Fraction(y))


def _sub(a: Point, b: Point) -> Point:
    return (a[0] - b[0], a[1] - b[1])


def _cross(u: Point, v: Point) -> Fraction:
    return u[0] * v[1] - u[1] * v[0]


def _dot(u: Point, v: Point) -> Fraction:
    return u[0] * v[0] + u[1] * v[1]


def _dist2(a: Point, b: Point) -> Fraction:
    d = _sub(a, b)
    return _dot(d, d)


def _exact_sqrt(value: Fraction):
    """Return sqrt(value) as a Fraction if it is rational, else None."""
    num = isqrt(value.numerator)
    den = isqrt(value.denominator)
    if num * num == value.numerator and den * den == value.denominator:
        return Fraction(num, den)
    return None


# ---------------------------------------------------------------------------
# Edges and supporting lines
# ---------------------------------------------------------------------------

def _ring_edges(vertices) -> list[tuple[Point, Point]]:
    pts = [as_point(v) for v in vertices]
    if len(pts) < 3:
        raise ValueError("a polygon needs at least 3 vertices")
    edges = []
    for i, a in enumerate(pts):
        b = pts[(i + 1) % len(pts)]
        if a != b:  # zero-length edge: dropped by definition
            edges.append((a, b))
    return edges


def _line_key(a: Point, b: Point) -> tuple[int, int, int]:
    """Canonical integer normal form (A, B, C) of the line A*x + B*y = C.

    Coefficients are coprime integers whose first nonzero entry is positive,
    so two collinear edges always produce the identical key.
    """
    na = a[1] - b[1]
    nb = b[0] - a[0]
    nc = na * a[0] + nb * a[1]
    den = 1
    for c in (na, nb, nc):
        den = den * c.denominator // gcd(den, c.denominator)
    ints = [int(c * den) for c in (na, nb, nc)]
    g = 0
    for v in ints:
        g = gcd(g, abs(v))
    ints = [v // g for v in ints]
    for v in ints:
        if v:
            if v < 0:
                ints = [-t for t in ints]
            break
    return (ints[0], ints[1], ints[2])


def _line_param(key: tuple[int, int, int], p: Point) -> Fraction:
    """Parameter of p along the line's canonical direction d = (-B, A)."""
    A, B, _ = key
    return -B * p[0] + A * p[1]


def _point_at_param(key: tuple[int, int, int], t: Fraction) -> Point:
    """Point on the line whose canonical parameter equals t (exact)."""
    A, B, C = key
    s = A * A + B * B
    return (
        Fraction(A * C - B * t, s),
        Fraction(B * C + A * t, s),
    )


def _point_on_segment(p: Point, a: Point, b: Point) -> bool:
    if _cross(_sub(b, a), _sub(p, a)) != 0:
        return False
    return (
        min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


# ---------------------------------------------------------------------------
# Shared-boundary computation
# ---------------------------------------------------------------------------

def _merge_intervals(intervals):
    """Union of 1-D intervals; touching intervals merge into one."""
    merged = []
    for lo, hi in sorted(intervals):
        if merged and lo <= merged[-1][1]:
            if hi > merged[-1][1]:
                merged[-1][1] = hi
        else:
            merged.append([lo, hi])
    return merged


def _shared_intervals(edges_a, edges_b):
    """Overlap intervals per supporting line, plus collinear touch points."""
    by_line = {}
    for a, b in edges_a:
        by_line.setdefault(_line_key(a, b), []).append((a, b))
    intervals = {}
    touch_points = []
    for c, d in edges_b:
        key = _line_key(c, d)
        if key not in by_line:
            continue
        tclo, tchi = sorted((_line_param(key, c), _line_param(key, d)))
        for a, b in by_line[key]:
            talo, tahi = sorted((_line_param(key, a), _line_param(key, b)))
            lo = max(talo, tclo)
            hi = min(tahi, tchi)
            if lo < hi:
                intervals.setdefault(key, []).append((lo, hi))
            elif lo == hi:
                touch_points.append(_point_at_param(key, lo))
    return intervals, touch_points


def _crossing_points(edges_a, edges_b):
    """Exact intersection points of non-parallel edge pairs."""
    pts = []
    for a, b in edges_a:
        u = _sub(b, a)
        for c, d in edges_b:
            v = _sub(d, c)
            den = _cross(u, v)
            if den == 0:
                continue  # parallel/collinear handled by _shared_intervals
            w = _sub(c, a)
            s = _cross(w, v) / den
            t = _cross(w, u) / den
            if 0 <= s <= 1 and 0 <= t <= 1:
                pts.append((a[0] + s * u[0], a[1] + s * u[1]))
    return pts


@dataclass(frozen=True)
class SharedSegment:
    """A maximal shared boundary segment with exact rational endpoints."""

    start: Point
    end: Point
    length_squared: Fraction

    @property
    def length(self):
        """Exact Fraction when the length is rational, else a float."""
        exact = _exact_sqrt(self.length_squared)
        return exact if exact is not None else sqrt(float(self.length_squared))


@dataclass(frozen=True)
class Adjacency:
    """Result of analyze_adjacency().

    shared_segments: maximal shared boundary segments (union of overlaps,
        duplicate edges already collapsed, contiguous pieces merged).
    point_contacts:  isolated boundary contact points, not lying on any
        shared segment. A point-only contact is not edge adjacency.
    """

    shared_segments: tuple[SharedSegment, ...]
    point_contacts: tuple[Point, ...]

    @property
    def is_edge_adjacent(self) -> bool:
        return bool(self.shared_segments)

    @property
    def shared_length(self):
        """Total shared boundary length (Fraction when exactly rational)."""
        total = Fraction(0)
        exact = True
        for seg in self.shared_segments:
            value = _exact_sqrt(seg.length_squared)
            if value is None:
                exact = False
                total = float(total) + sqrt(float(seg.length_squared))
            else:
                total += value
        return total if exact else float(total)


def analyze_adjacency(poly_a, poly_b) -> Adjacency:
    """Determine shared-edge adjacency between two closed polygon rings.

    Vertices may be int/Fraction sequences. The result is invariant under
    vertex order direction, inserted collinear vertices, and duplicated
    edges; shared length is the measure of the boundary intersection and is
    never double counted.
    """
    edges_a = _ring_edges(poly_a)
    edges_b = _ring_edges(poly_b)

    intervals, touch_points = _shared_intervals(edges_a, edges_b)

    segments = []
    for key, ivs in intervals.items():
        A, B, _ = key
        norm_sq = Fraction(A * A + B * B)
        for lo, hi in _merge_intervals(ivs):
            segments.append(
                SharedSegment(
                    start=_point_at_param(key, lo),
                    end=_point_at_param(key, hi),
                    length_squared=(hi - lo) ** 2 / norm_sq,
                )
            )
    segments.sort(key=lambda s: (s.start, s.end))

    candidates = touch_points + _crossing_points(edges_a, edges_b)
    contacts = []
    seen = set()
    for p in candidates:
        if p in seen:
            continue
        on_shared = any(
            _point_on_segment(p, seg.start, seg.end) for seg in segments
        )
        if not on_shared:
            seen.add(p)
            contacts.append(p)
    contacts.sort()

    return Adjacency(
        shared_segments=tuple(segments),
        point_contacts=tuple(contacts),
    )


# ---------------------------------------------------------------------------
# Tolerance snapping
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class SnapEvent:
    """Record of one snapped vertex: displacement is reported exactly."""

    index: int
    original: Point
    result: Point

    @property
    def displacement(self) -> Point:
        return _sub(self.result, self.original)

    @property
    def distance_squared(self) -> Fraction:
        return _dist2(self.original, self.result)

    @property
    def moved(self) -> bool:
        return self.original != self.result


def snap_vertices(vertices, targets, tolerance) -> tuple[list[Point], list[SnapEvent]]:
    """Snap each vertex to the nearest target within tolerance.

    Exactness contract:
      * The within-tolerance test is distance_squared <= tolerance**2,
        evaluated exactly on rationals -- no epsilon.
      * Every moved vertex produces a SnapEvent reporting its exact
        displacement; unmoved vertices are returned bit-identical, so
        topology unrelated to a snapped vertex is never altered.
      * Ties between equidistant targets resolve to the lowest target
        index, keeping the operation deterministic.
    """
    tol = Fraction(tolerance)
    if tol < 0:
        raise ValueError("tolerance must be non-negative")
    tol_sq = tol * tol
    target_pts = [as_point(t) for t in targets]

    snapped = []
    events = []
    for i, raw in enumerate(vertices):
        p = as_point(raw)
        best = None
        best_d2 = None
        for cand in target_pts:
            d2 = _dist2(p, cand)
            if d2 <= tol_sq and (best_d2 is None or d2 < best_d2):
                best = cand
                best_d2 = d2
        if best is None:
            snapped.append(p)
        else:
            snapped.append(best)
            events.append(SnapEvent(index=i, original=p, result=best))
    return snapped, events


def snap_polygon_to_polygon(moving, anchor, tolerance):
    """Snap moving's vertices onto anchor's vertices within tolerance.

    Returns (snapped_vertices, events). Only vertices listed in events
    changed; every other vertex is exactly the input vertex.
    """
    return snap_vertices(moving, [as_point(v) for v in anchor], tolerance)
