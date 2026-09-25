"""Minimum-area oriented bounding rectangle for 2D point sets.

All arithmetic is exact (``fractions.Fraction``); no floating point and no
epsilon-based topological decisions anywhere.  Coordinates may be ints,
Fractions, or anything ``Fraction`` accepts (e.g. "1/3", decimal strings).

Tie-breaking (canonical selection among equal-area optima), applied in order:
  1. smaller exact area;
  2. smaller orientation angle in [0, pi), compared exactly via half-plane
     normalisation of the edge direction plus a cross-product sign test;
  3. lexicographically smallest tuple of the four corners (exact Fractions).

Degenerate inputs are defined, not patched:
  * empty input             -> ValueError
  * a single distinct point -> zero-area rectangle collapsed on the point
  * all points collinear    -> zero-area rectangle on the extreme segment
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, Iterable, List, Sequence, Tuple

Point = Tuple[Fraction, Fraction]


def to_point(p) -> Point:
    """Normalise a 2-sequence of int/Fraction/rational-like to exact form."""
    x, y = p
    return (Fraction(x), Fraction(y))


def _sub(a: Point, b: Point) -> Point:
    return (a[0] - b[0], a[1] - b[1])


def _cross(o: Point, a: Point, b: Point) -> Fraction:
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


def _dot(p: Point, d: Point) -> Fraction:
    return p[0] * d[0] + p[1] * d[1]


def convex_hull(points: Sequence[Point]) -> List[Point]:
    """Monotone chain; returns CCW hull vertices, no collinear intermediates.

    Returns 1 vertex if all points coincide, 2 if all are collinear.
    """
    pts = sorted(set(points))
    if len(pts) <= 1:
        return pts
    lower: List[Point] = []
    for p in pts:
        while len(lower) >= 2 and _cross(lower[-2], lower[-1], p) <= 0:
            lower.pop()
        lower.append(p)
    upper: List[Point] = []
    for p in reversed(pts):
        while len(upper) >= 2 and _cross(upper[-2], upper[-1], p) <= 0:
            upper.pop()
        upper.append(p)
    return lower[:-1] + upper[:-1]


def _normalise_direction(d: Point) -> Point:
    """Map a direction to its canonical representative in [0, pi)."""
    dx, dy = d
    if dx < 0 or (dx == 0 and dy < 0):
        return (-dx, -dy)
    return (dx, dy)


def _angle_less(d1: Point, d2: Point) -> bool:
    """Exact comparison of two directions' angles, both in [0, pi)."""
    a = _normalise_direction(d1)
    b = _normalise_direction(d2)
    if a == b:
        return False
    # Both lie in the closed upper half-plane; cross > 0 means a before b.
    c = a[0] * b[1] - a[1] * b[0]
    return c > 0


@dataclass(frozen=True)
class Certificate:
    """Verifiable witness for the minimum-area oriented bounding rectangle."""

    area: Fraction
    corners: Tuple[Point, Point, Point, Point]  # CCW
    direction: Point          # edge direction u (unnormalised, exact)
    supporting_edge: Tuple[Point, Point]  # hull edge collinear with a side
    extreme_points: Dict[str, Point]      # u_min/u_max/v_min/v_max argextrema
    u_range: Tuple[Fraction, Fraction]    # min/max of p.u over the point set
    v_range: Tuple[Fraction, Fraction]    # min/max of p.v over the point set


def _corners_from_ranges(u: Point, v: Point, den: Fraction,
                         umin: Fraction, umax: Fraction,
                         vmin: Fraction, vmax: Fraction) -> Tuple[Point, ...]:
    """Recover rectangle corners from projection ranges.

    u and v are orthogonal, so p = (U*u + V*v) / |u|^2 solves
    p.u = U, p.v = V exactly.
    """
    def corner(U: Fraction, V: Fraction) -> Point:
        return ((U * u[0] + V * v[0]) / den, (U * u[1] + V * v[1]) / den)

    return (
        corner(umin, vmin),
        corner(umax, vmin),
        corner(umax, vmax),
        corner(umin, vmax),
    )


def _build_certificate(pts: List[Point], hull: List[Point], i: int,
                       umin: Fraction, umax: Fraction,
                       vmin: Fraction, vmax: Fraction) -> Certificate:
    m = len(hull)
    a, b = hull[i], hull[(i + 1) % m]
    u = _sub(b, a)
    v = (-u[1], u[0])
    den = u[0] * u[0] + u[1] * u[1]
    area = (umax - umin) * (vmax - vmin) / den
    corners = tuple(_corners_from_ranges(u, v, den, umin, umax, vmin, vmax))
    # When several input points attain an extreme (a supporting plateau),
    # resolve the witness lexicographically: identical certificates must
    # not depend on the calipers pointer or the insertion order.
    def witness(value: Fraction, d: Point) -> Point:
        return min(p for p in pts if _dot(p, d) == value)

    return Certificate(
        area=area,
        corners=corners,  # type: ignore[arg-type]
        direction=u,
        supporting_edge=(a, b),
        extreme_points={
            "u_min": witness(umin, u),
            "u_max": witness(umax, u),
            "v_min": witness(vmin, v),
            "v_max": witness(vmax, v),
        },
        u_range=(umin, umax),
        v_range=(vmin, vmax),
    )


def _is_better(cand: Certificate, best: Certificate) -> bool:
    if cand.area != best.area:
        return cand.area < best.area
    na = _normalise_direction(cand.direction)
    nb = _normalise_direction(best.direction)
    if na != nb:
        return _angle_less(cand.direction, best.direction)
    return cand.corners < best.corners


def min_area_rectangle(points: Sequence) -> Certificate:
    """Exact minimum-area oriented bounding rectangle via rotating calipers."""
    pts = [to_point(p) for p in points]
    if not pts:
        raise ValueError("minimum-area rectangle is undefined for an empty point set")
    hull = convex_hull(pts)
    m = len(hull)

    if m == 1:
        p = hull[0]
        return Certificate(
            area=Fraction(0),
            corners=(p, p, p, p),
            direction=(Fraction(1), Fraction(0)),
            supporting_edge=(p, p),
            extreme_points={"u_min": p, "u_max": p, "v_min": p, "v_max": p},
            u_range=(p[0], p[0]),
            v_range=(p[1], p[1]),
        )

    # Edge 0: locate all four extreme vertices by an exact full scan.
    a0, b0 = hull[0], hull[1]
    u0 = _sub(b0, a0)
    v0 = (-u0[1], u0[0])
    iu_max = max(range(m), key=lambda j: _dot(hull[j], u0))
    iu_min = min(range(m), key=lambda j: _dot(hull[j], u0))
    iv_max = max(range(m), key=lambda j: _dot(hull[j], v0))
    iv_min = min(range(m), key=lambda j: _dot(hull[j], v0))

    # Subsequent edges: as the edge direction rotates CCW around the hull,
    # each extremal vertex also advances monotonically CCW, so the four
    # pointers sweep the hull in total O(m) steps.
    best: Certificate | None = None
    for i in range(m):
        a, b = hull[i], hull[(i + 1) % m]
        u = _sub(b, a)
        v = (-u[1], u[0])

        def du(j: int) -> Fraction:
            return _dot(hull[j], u)

        def dv(j: int) -> Fraction:
            return _dot(hull[j], v)

        # Advance across ties with >= / <= so a pointer carried into a
        # supporting plateau reaches its trailing end; the m-step budget
        # makes the walk terminate even if every projection is equal.
        for _ in range(m):
            nxt = (iu_max + 1) % m
            if du(nxt) < du(iu_max):
                break
            iu_max = nxt
        for _ in range(m):
            nxt = (iu_min + 1) % m
            if du(nxt) > du(iu_min):
                break
            iu_min = nxt
        for _ in range(m):
            nxt = (iv_max + 1) % m
            if dv(nxt) < dv(iv_max):
                break
            iv_max = nxt
        for _ in range(m):
            nxt = (iv_min + 1) % m
            if dv(nxt) > dv(iv_min):
                break
            iv_min = nxt

        cand = _build_certificate(
            pts, hull, i,
            _dot(hull[iu_min], u), _dot(hull[iu_max], u),
            _dot(hull[iv_min], v), _dot(hull[iv_max], v),
        )
        if best is None or _is_better(cand, best):
            best = cand

    assert best is not None
    return best


def brute_force_min_area(points: Sequence) -> Certificate:
    """Reference: enumerate every hull edge, recompute extremes per edge.

    O(m * n) and pointer-free; used to cross-check the rotating calipers.
    """
    pts = [to_point(p) for p in points]
    if not pts:
        raise ValueError("minimum-area rectangle is undefined for an empty point set")
    hull = convex_hull(pts)
    if len(hull) == 1:
        return min_area_rectangle(pts)
    best: Certificate | None = None
    for i in range(len(hull)):
        a, b = hull[i], hull[(i + 1) % len(hull)]
        u = _sub(b, a)
        v = (-u[1], u[0])
        den = u[0] * u[0] + u[1] * u[1]
        us = [_dot(p, u) for p in pts]
        vs = [_dot(p, v) for p in pts]
        umin, umax = min(us), max(us)
        vmin, vmax = min(vs), max(vs)
        area = (umax - umin) * (vmax - vmin) / den
        cand = _build_certificate(pts, hull, i, umin, umax, vmin, vmax)
        if best is None or _is_better(cand, best):
            best = cand
    assert best is not None
    return best


def polygon_area(corners: Sequence[Point]) -> Fraction:
    """Exact signed shoelace area of a polygon."""
    s = Fraction(0)
    n = len(corners)
    for i in range(n):
        x1, y1 = corners[i]
        x2, y2 = corners[(i + 1) % n]
        s += x1 * y2 - x2 * y1
    return s / 2


def verify_certificate(cert: Certificate, points: Sequence) -> bool:
    """Check a certificate against the point set, all exactly.

    Verifies: non-empty input, optimality against per-edge brute force,
    projection ranges match the true extremes, extreme points are input
    points, the supporting edge is a hull edge collinear with the
    direction, and the four corners recover a rectangle of equal area.
    """
    pts = [to_point(p) for p in points]
    if not pts:
        raise ValueError("cannot certify an empty point set")

    # Optimality: certificate area must equal the brute-force optimum.
    if cert.area != brute_force_min_area(pts).area:
        return False

    # Projection ranges must be the true extremes over the point set.
    u = cert.direction
    v = (-u[1], u[0])
    us = [_dot(p, u) for p in pts]
    vs = [_dot(p, v) for p in pts]
    if cert.u_range != (min(us), max(us)):
        return False
    if cert.v_range != (min(vs), max(vs)):
        return False

    # Extreme points must be actual input points attaining the ranges.
    ptset = set(pts)
    for key, val in (("u_min", cert.u_range[0]), ("u_max", cert.u_range[1])):
        p = cert.extreme_points[key]
        if p not in ptset or _dot(p, u) != val:
            return False
    for key, val in (("v_min", cert.v_range[0]), ("v_max", cert.v_range[1])):
        p = cert.extreme_points[key]
        if p not in ptset or _dot(p, v) != val:
            return False

    # Supporting edge must be a hull edge parallel to the direction.
    hull = convex_hull(pts)
    a, b = cert.supporting_edge
    if len(hull) == 1:
        if not (a == b == hull[0]):
            return False
    else:
        m = len(hull)
        edges = {(hull[i], hull[(i + 1) % m]) for i in range(m)}
        if (a, b) not in edges:
            return False
        e = _sub(b, a)
        if e[0] * u[1] - e[1] * u[0] != 0:
            return False

    # The four corners must recover a rectangle of exactly equal area,
    # and the area can never be negative (exact arithmetic, no epsilon).
    if polygon_area(cert.corners) != cert.area:
        return False
    if cert.area < 0:
        return False
    return True


class PointSet:
    """Mutable point set with per-insertion identity and batch withdrawal.

    Duplicate coordinates are allowed: every insertion gets a distinct id,
    so duplicates can be withdrawn individually.
    """

    def __init__(self, points: Iterable = ()):
        self._pts: Dict[int, Point] = {}
        self._next_id = 0
        self.add_many(points)

    def add(self, p) -> int:
        pid = self._next_id
        self._next_id += 1
        self._pts[pid] = to_point(p)
        return pid

    def add_many(self, points: Iterable) -> List[int]:
        return [self.add(p) for p in points]

    def remove_many(self, ids: Iterable[int]) -> None:
        """Batch-withdraw points by id; unknown ids raise KeyError."""
        ids = list(ids)
        missing = [i for i in ids if i not in self._pts]
        if missing:
            raise KeyError(f"unknown point ids: {missing}")
        for i in ids:
            del self._pts[i]

    def __len__(self) -> int:
        return len(self._pts)

    def points(self) -> List[Point]:
        return list(self._pts.values())

    def minimum_area_rectangle(self) -> Certificate:
        return min_area_rectangle(self.points())
