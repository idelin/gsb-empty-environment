"""Exact rational geometry for segment distance queries.

All coordinates are integers or ``fractions.Fraction``.  Every distance
comparison is performed on *squared* distances, so no square roots and no
floating point are ever involved.  There is deliberately no epsilon
anywhere: degenerate configurations (crossing segments, zero-length
segments, parallel equidistant segments, collinear overlaps) are decided
by exact rational arithmetic and have precisely defined outputs.

Closest-point parameterisation
------------------------------
A segment ``p0 -> p1`` is parameterised by ``t`` in ``[0, 1]`` with
``P(t) = p0 + t * (p1 - p0)``.

The minimiser of the squared distance between two segments is either

* a single parameter pair ``(s, t)`` -- represented as a degenerate
  interval where ``lo == hi`` --, or
* a continuum (only possible for parallel segments whose projections
  overlap at equal distance, which includes collinear overlaps at
  distance 0).  It is represented as the parameter interval
  ``[(s_lo, t_lo), (s_hi, t_hi)]`` with the endpoints ordered
  lexicographically by ``(s, t)``.  Every convex combination of the two
  endpoints is a minimiser.
"""

from __future__ import annotations

from fractions import Fraction

ZERO = Fraction(0)
ONE = Fraction(1)

Point = tuple  # (Fraction, Fraction)


def frac(value) -> Fraction:
    """Coerce an int/Fraction to ``Fraction`` (exact)."""
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


def point(x, y) -> Point:
    return (frac(x), frac(y))


def _sub(a, b):
    return (a[0] - b[0], a[1] - b[1])


def _dot(a, b):
    return a[0] * b[0] + a[1] * b[1]


def norm2(v) -> Fraction:
    return _dot(v, v)


def eval_point(p0, p1, t) -> Point:
    """Point at parameter ``t`` on segment ``p0 -> p1`` (exact)."""
    return (p0[0] + (p1[0] - p0[0]) * t, p0[1] + (p1[1] - p0[1]) * t)


def clamp01(x: Fraction) -> Fraction:
    if x < ZERO:
        return ZERO
    if x > ONE:
        return ONE
    return x


class Minimizer:
    """Exact minimiser of the squared distance between two segments.

    ``dist2`` is the squared minimal distance (a ``Fraction``).
    ``(s_lo, t_lo)`` and ``(s_hi, t_hi)`` describe the set of minimising
    parameters; for a unique minimiser ``lo == hi``.  Endpoints are
    ordered lexicographically by ``(s, t)``.
    """

    __slots__ = ("dist2", "s_lo", "t_lo", "s_hi", "t_hi")

    def __init__(self, dist2, s_lo, t_lo, s_hi, t_hi):
        # Canonical order: lexicographic by (s, t).
        if (s_lo, t_lo) > (s_hi, t_hi):
            s_lo, t_lo, s_hi, t_hi = s_hi, t_hi, s_lo, t_lo
        self.dist2 = dist2
        self.s_lo = s_lo
        self.t_lo = t_lo
        self.s_hi = s_hi
        self.t_hi = t_hi

    @property
    def is_interval(self) -> bool:
        return (self.s_lo, self.t_lo) != (self.s_hi, self.t_hi)

    def __repr__(self):
        return (
            f"Minimizer(dist2={self.dist2}, "
            f"s=[{self.s_lo},{self.s_hi}], t=[{self.t_lo},{self.t_hi}])"
        )


def closest_params(p0, p1, q0, q1) -> Minimizer:
    """Exact closest-point parameters between segments p0->p1 and q0->q1.

    Minimises ``|r + s*u - t*v|^2`` over ``(s, t)`` in ``[0,1]^2`` where
    ``u = p1 - p0``, ``v = q1 - q0``, ``r = p0 - q0``.

    Degenerate cases (all decided exactly, no epsilon):

    * both segments zero-length -> point/point distance, ``s == t == 0``;
    * one segment zero-length -> point/segment distance, unique minimiser;
    * crossing or touching segments -> ``dist2 == 0`` with exact rational
      parameters of the intersection;
    * parallel segments -> the minimiser set is the exact intersection of
      the line ``s - lambda*t = w*`` with the parameter box, returned as
      an interval (or a single point when the intersection is a corner).
    """
    u = _sub(p1, p0)
    v = _sub(q1, q0)
    r = _sub(p0, q0)
    A = _dot(u, u)   # |u|^2
    E = _dot(v, v)   # |v|^2
    B = _dot(u, v)
    C = _dot(u, r)
    F = _dot(v, r)
    R2 = _dot(r, r)

    def dist2(s, t):
        return R2 + 2 * s * C - 2 * t * F + s * s * A + t * t * E - 2 * s * t * B

    if A == 0 and E == 0:
        # Two zero-length segments: two points.
        return Minimizer(R2, ZERO, ZERO, ZERO, ZERO)
    if A == 0:
        # p is a point; unique closest parameter on q (strictly convex).
        t = clamp01(F / E)
        return Minimizer(dist2(ZERO, t), ZERO, t, ZERO, t)
    if E == 0:
        # q is a point.
        s = clamp01(-C / A)
        return Minimizer(dist2(s, ZERO), s, ZERO, s, ZERO)

    D = A * E - B * B
    if D != 0:
        # Non-parallel: strictly convex quadratic -> unique minimiser.
        s_star = (B * F - C * E) / D
        t_star = (A * F - B * C) / D
        if ZERO <= s_star <= ONE and ZERO <= t_star <= ONE:
            s, t = s_star, t_star
        else:
            # Constrained minimum lies on the box boundary; check the four
            # edges exactly.  Ties can only occur at corners (same point
            # reached from two edges); the lexicographic tie-break keeps
            # the result deterministic.
            candidates = [
                (ZERO, clamp01(F / E)),
                (ONE, clamp01((F + B) / E)),
                (clamp01(-C / A), ZERO),
                (clamp01((B - C) / A), ONE),
            ]
            s, t = min(candidates, key=lambda st: (dist2(*st), st[0], st[1]))
        return Minimizer(dist2(s, t), s, t, s, t)

    # Parallel segments (D == 0, A > 0, E > 0 => B != 0).
    # With v = lambda*u the distance depends only on w = s - lambda*t:
    #   f(w) = |r|^2 + 2*w*C + w^2*A,  unconstrained min at w* = -C/A.
    lam = B / A
    w_star = -C / A
    # Range of w over the parameter box (attained at corners).
    w_lo = min(ZERO, -lam)
    w_hi = max(ONE, ONE - lam)
    if w_star < w_lo:
        # Unique corner minimiser.
        s, t = (ZERO, ONE) if lam > 0 else (ZERO, ZERO)
        return Minimizer(dist2(s, t), s, t, s, t)
    if w_star > w_hi:
        s, t = (ONE, ZERO) if lam > 0 else (ONE, ONE)
        return Minimizer(dist2(s, t), s, t, s, t)
    # w* is attainable: minimiser set is the segment s = w* + lam*t
    # clipped to the box (an interval, possibly a single corner point).
    if lam > 0:
        t_lo = max(ZERO, -w_star / lam)
        t_hi = min(ONE, (ONE - w_star) / lam)
    else:
        t_lo = max(ZERO, (ONE - w_star) / lam)
        t_hi = min(ONE, -w_star / lam)
    s_lo = w_star + lam * t_lo
    s_hi = w_star + lam * t_hi
    return Minimizer(dist2(s_lo, t_lo), s_lo, t_lo, s_hi, t_hi)


# ---------------------------------------------------------------------------
# Axis-aligned bounding boxes (exact Fraction coordinates).
# bbox = (min_x, min_y, max_x, max_y)
# ---------------------------------------------------------------------------

def bbox_of_segment(p0, p1):
    return (
        min(p0[0], p1[0]),
        min(p0[1], p1[1]),
        max(p0[0], p1[0]),
        max(p0[1], p1[1]),
    )


def bbox_union(b1, b2):
    return (
        min(b1[0], b2[0]),
        min(b1[1], b2[1]),
        max(b1[2], b2[2]),
        max(b1[3], b2[3]),
    )


def bbox_dist2(b1, b2) -> Fraction:
    """Exact squared distance between two AABBs (0 when they overlap)."""
    dx = max(b1[0] - b2[2], b2[0] - b1[2], ZERO)
    dy = max(b1[1] - b2[3], b2[1] - b1[3], ZERO)
    return dx * dx + dy * dy
