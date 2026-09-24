"""Flatten quadratic/cubic Bezier curves into polylines with a certified error bound.

All arithmetic is exact rational arithmetic (``fractions.Fraction``).  Flatness
decisions use a closed-form *geometric* bound on the distance between the
curve and its chord segment -- no epsilon tuning is involved anywhere.

The bound
---------
Let the control points be P_0..P_n (n = 2 or 3) and let the chord run from
P_0 to P_n with direction w = P_n - P_0 and squared length L2 = |w|^2.
Every curve point B(t) is a *convex* combination of the control points, so
its tangential and normal coordinates with respect to the chord lie within
the control points' ranges.  With

    s_i     = (P_i - P_0) . w          (unnormalized tangential coordinate)
    c_i     = (P_i - P_0) x w          (unnormalized normal coordinate)
    DU^2    = max_i dist(s_i, [0, L2])^2
    DC^2    = max_i c_i^2

the distance from any curve point to the chord *segment* satisfies

    dist(B(t), chord)  <=  sqrt(DU^2 + DC^2) / L        (L = sqrt(L2))

which is certified by the exact rational test  ``DU^2 + DC^2 <= tol^2 * L2``.
If the chord degenerates to a point (P_0 == P_n), the bound is simply
``max_i |P_i - P_0|``.  No square roots are ever computed.

Consequences, by construction rather than by epsilon patching:

* Collinear, monotonically ordered control points give DU = DC = 0, so a
  straight line is always emitted as a single segment.  (Collinear control
  points whose curve *overshoots* the chord have DU > 0 and correctly keep
  subdividing -- the polyline must cover the overshoot.)
* A cusp (vanishing first derivative) keeps the distance bound positive, so
  subdivision naturally continues around the cusp parameter.
* If ``max_depth`` is reached while the bound still exceeds the tolerance,
  the offending parameter intervals are reported via
  ``FlattenResult.unmet_intervals`` and ``converged`` is ``False``.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Tuple

Point = Tuple[Fraction, Fraction]

__all__ = [
    "Point",
    "FlattenResult",
    "flatten",
    "flatten_quadratic",
    "flatten_cubic",
]


def _as_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError(f"not a rational coordinate: {value!r}")
    try:
        return Fraction(value)
    except (TypeError, ValueError) as exc:
        raise TypeError(f"not a rational coordinate: {value!r}") from exc


def _as_point(p) -> Point:
    try:
        x, y = p
    except (TypeError, ValueError) as exc:
        raise TypeError(f"not a 2D control point: {p!r}") from exc
    return (_as_fraction(x), _as_fraction(y))


def _mid(a: Point, b: Point) -> Point:
    return ((a[0] + b[0]) / 2, (a[1] + b[1]) / 2)


def _split_quad(ctrl):
    p0, p1, p2 = ctrl
    q0 = _mid(p0, p1)
    q1 = _mid(p1, p2)
    r = _mid(q0, q1)
    return (p0, q0, r), (r, q1, p2)


def _split_cubic(ctrl):
    p0, p1, p2, p3 = ctrl
    q0 = _mid(p0, p1)
    q1 = _mid(p1, p2)
    q2 = _mid(p2, p3)
    r0 = _mid(q0, q1)
    r1 = _mid(q1, q2)
    s = _mid(r0, r1)
    return (p0, q0, r0, s), (s, r1, q2, p3)


def _chord_bound(ctrl):
    """Certified squared distance bound from curve to chord segment.

    Returns ``(num, den)`` such that dist(curve, chord segment)^2 <= num/den,
    with exact rational arithmetic.  See the module docstring for the proof
    sketch.
    """
    p0 = ctrl[0]
    pn = ctrl[-1]
    wx = pn[0] - p0[0]
    wy = pn[1] - p0[1]
    l2 = wx * wx + wy * wy
    if l2 == 0:
        # Degenerate chord: the "segment" is the single point P_0, and every
        # curve point is a convex combination of the control points.
        num = max(
            (p[0] - p0[0]) ** 2 + (p[1] - p0[1]) ** 2 for p in ctrl
        )
        return num, Fraction(1)
    du = Fraction(0)
    dc = Fraction(0)
    for p in ctrl:
        rx = p[0] - p0[0]
        ry = p[1] - p0[1]
        s = rx * wx + ry * wy
        if s < 0:
            d = s * s
        elif s > l2:
            d = (s - l2) ** 2
        else:
            d = Fraction(0)
        if d > du:
            du = d
        c = rx * wy - ry * wx
        c2 = c * c
        if c2 > dc:
            dc = c2
    return du + dc, l2


def _is_flat(ctrl, tol_sq: Fraction) -> bool:
    num, den = _chord_bound(ctrl)
    return num <= tol_sq * den


@dataclass(frozen=True)
class FlattenResult:
    """Polyline approximation of a Bezier curve.

    ``points[i]`` is the curve evaluated exactly at ``parameters[i]``;
    consecutive pairs share endpoints, so the polyline is continuous by
    construction.  ``parameters`` starts at 0, ends at 1 and is strictly
    increasing (dyadic rationals).  If the depth cap stopped subdivision
    before the tolerance was met, ``converged`` is False and
    ``unmet_intervals`` lists the parameter intervals that violate it.
    """

    points: Tuple[Point, ...]
    parameters: Tuple[Fraction, ...]
    converged: bool
    unmet_intervals: Tuple[Tuple[Fraction, Fraction], ...]
    max_depth: int

    @property
    def intervals(self) -> Tuple[Tuple[Fraction, Fraction], ...]:
        """Parameter intervals (t_i, t_{i+1}) covered by each polyline segment."""
        return tuple(zip(self.parameters, self.parameters[1:]))

    @property
    def segments(self) -> Tuple[Tuple[Point, Point], ...]:
        """Polyline segments as (start, end) point pairs."""
        return tuple(zip(self.points, self.points[1:]))


def _flatten(ctrl, tolerance, max_depth, split) -> FlattenResult:
    tol = _as_fraction(tolerance)
    if tol <= 0:
        raise ValueError("tolerance must be a positive rational")
    if isinstance(max_depth, bool) or not isinstance(max_depth, int):
        raise TypeError("max_depth must be an integer")
    if max_depth < 0:
        raise ValueError("max_depth must be >= 0")

    tol_sq = tol * tol
    points = [ctrl[0]]
    params = [Fraction(0)]
    unmet = []

    def recurse(c, t0, t1, depth):
        if _is_flat(c, tol_sq):
            points.append(c[-1])
            params.append(t1)
            return
        if depth >= max_depth:
            points.append(c[-1])
            params.append(t1)
            unmet.append((t0, t1))
            return
        left, right = split(c)
        tm = (t0 + t1) / 2
        recurse(left, t0, tm, depth + 1)
        recurse(right, tm, t1, depth + 1)

    recurse(ctrl, Fraction(0), Fraction(1), 0)
    return FlattenResult(
        points=tuple(points),
        parameters=tuple(params),
        converged=not unmet,
        unmet_intervals=tuple(unmet),
        max_depth=max_depth,
    )


def flatten(control_points: Iterable, tolerance, max_depth: int = 32) -> FlattenResult:
    """Flatten a quadratic (3 points) or cubic (4 points) Bezier curve.

    ``control_points`` are 2D points whose coordinates may be ``int``,
    ``Fraction``, ``float`` (converted to its exact binary value) or numeric
    strings.  ``tolerance`` is the certified maximum distance between the
    curve and the returned polyline.  ``max_depth`` caps bisection depth;
    intervals that still violate the tolerance at the cap are reported in
    the result instead of being silently accepted.
    """
    pts = tuple(_as_point(p) for p in control_points)
    if len(pts) == 3:
        return _flatten(pts, tolerance, max_depth, _split_quad)
    if len(pts) == 4:
        return _flatten(pts, tolerance, max_depth, _split_cubic)
    raise ValueError(
        f"expected 3 (quadratic) or 4 (cubic) control points, got {len(pts)}"
    )


def flatten_quadratic(p0, p1, p2, tolerance, max_depth: int = 32) -> FlattenResult:
    return flatten((p0, p1, p2), tolerance, max_depth)


def flatten_cubic(p0, p1, p2, p3, tolerance, max_depth: int = 32) -> FlattenResult:
    return flatten((p0, p1, p2, p3), tolerance, max_depth)
