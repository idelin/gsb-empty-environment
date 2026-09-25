"""Exact planar geometry on rational coordinates.

All coordinates are ``fractions.Fraction`` (plain ``int`` is accepted and
converted).  Every predicate is evaluated with exact rational arithmetic, so
topological decisions (crossing / touching / collinearity) never depend on
floating-point rounding or ad-hoc epsilon tolerances.
"""

from fractions import Fraction

__all__ = [
    "coord",
    "point",
    "cross",
    "orient",
    "on_segment",
    "line_intersection",
    "segments_relation",
    "dist2_point_segment",
    "twice_signed_area",
    "NONE",
    "CROSS",
    "TOUCH",
    "OVERLAP",
]

# Relation kinds returned by segments_relation.
NONE = "none"        # segments are disjoint
CROSS = "cross"      # proper crossing: one interior/interior intersection point
TOUCH = "touch"      # intersection is a finite set of boundary points
OVERLAP = "overlap"  # collinear and the intersection is 1-dimensional


def coord(value):
    """Convert an exact coordinate (int or Fraction) to Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        "coordinates must be int or fractions.Fraction, "
        f"got {type(value).__name__}; floats are rejected to keep "
        "arithmetic exact"
    )


def point(p):
    """Normalize an (x, y) pair to exact Fraction coordinates."""
    return (coord(p[0]), coord(p[1]))


def cross(u, v):
    return u[0] * v[1] - u[1] * v[0]


def orient(a, b, c):
    """Exact sign of the oriented area of triangle (a, b, c): -1, 0 or 1."""
    v = cross((b[0] - a[0], b[1] - a[1]), (c[0] - a[0], c[1] - a[1]))
    return (v > 0) - (v < 0)


def on_segment(a, b, p):
    """True if p lies on segment ab.  p must be collinear with a and b."""
    return (
        min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


def line_intersection(a, b, c, d):
    """Exact intersection point of lines ab and cd (must not be parallel)."""
    x1, y1 = a
    x2, y2 = b
    x3, y3 = c
    x4, y4 = d
    den = (x1 - x2) * (y3 - y4) - (y1 - y2) * (x3 - x4)
    if den == 0:
        raise ValueError("parallel or coincident lines have no unique intersection")
    e1 = x1 * y2 - y1 * x2
    e2 = x3 * y4 - y3 * x4
    return (
        (e1 * (x3 - x4) - (x1 - x2) * e2) / den,
        (e1 * (y3 - y4) - (y1 - y2) * e2) / den,
    )


def segments_relation(a, b, c, d):
    """Classify the intersection of closed segments ab and cd, exactly.

    Returns one of:
      (NONE,)
      (CROSS, pt)          proper interior crossing at pt
      (TOUCH, frozenset)   intersection is a set of isolated points
      (OVERLAP, lo, hi)    collinear overlap from lo to hi (lo != hi)
    """
    o1 = orient(a, b, c)
    o2 = orient(a, b, d)
    o3 = orient(c, d, a)
    o4 = orient(c, d, b)
    if o1 == 0 and o2 == 0 and o3 == 0 and o4 == 0:
        # Collinear (this also covers degenerate zero-length segments):
        # lexicographic order on (x, y) is a linear order along the line.
        ab = sorted((a, b))
        cd = sorted((c, d))
        lo = max(ab[0], cd[0])
        hi = min(ab[1], cd[1])
        if lo > hi:
            return (NONE,)
        if lo == hi:
            return (TOUCH, frozenset((lo,)))
        return (OVERLAP, lo, hi)
    if o1 * o2 < 0 and o3 * o4 < 0:
        return (CROSS, line_intersection(a, b, c, d))
    touches = set()
    if o3 == 0 and on_segment(c, d, a):
        touches.add(a)
    if o4 == 0 and on_segment(c, d, b):
        touches.add(b)
    if o1 == 0 and on_segment(a, b, c):
        touches.add(c)
    if o2 == 0 and on_segment(a, b, d):
        touches.add(d)
    if touches:
        return (TOUCH, frozenset(touches))
    return (NONE,)


def dist2_point_segment(p, a, b):
    """Exact squared distance from point p to segment ab (a Fraction)."""
    vx = b[0] - a[0]
    vy = b[1] - a[1]
    wx = p[0] - a[0]
    wy = p[1] - a[1]
    len2 = vx * vx + vy * vy
    if len2 == 0:
        return wx * wx + wy * wy
    t = wx * vx + wy * vy
    if t <= 0:
        return wx * wx + wy * wy
    if t >= len2:
        dx = p[0] - b[0]
        dy = p[1] - b[1]
        return dx * dx + dy * dy
    c = cross((vx, vy), (wx, wy))
    return Fraction(c * c, len2)


def twice_signed_area(pts):
    """Exact doubled signed area of a polygon given without repeated closure."""
    total = Fraction(0)
    n = len(pts)
    for i in range(n):
        x1, y1 = pts[i]
        x2, y2 = pts[(i + 1) % n]
        total += x1 * y2 - x2 * y1
    return total
