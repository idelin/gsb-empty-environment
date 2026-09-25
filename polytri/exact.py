"""Exact planar geometry predicates for integer / rational coordinates.

Every coordinate is stored as ``fractions.Fraction`` and every predicate is
evaluated with exact rational arithmetic.  No floating point and no epsilon
tolerances are used anywhere: collinearity, point-on-edge and point-in-polygon
are decided symbolically, so all topological decisions are deterministic and
reproducible.

Points are plain ``(Fraction, Fraction)`` tuples.  Rings are lists of points
without a repeated closing vertex.
"""

from fractions import Fraction

INSIDE = "inside"
OUTSIDE = "outside"
BOUNDARY = "boundary"


def to_fraction(value):
    """Convert an int / Fraction coordinate to Fraction (exact)."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        "coordinates must be int or fractions.Fraction, "
        f"got {type(value).__name__}"
    )


def to_point(p):
    """Convert a 2-sequence of int / Fraction to an exact point."""
    x, y = p
    return (to_fraction(x), to_fraction(y))


def orient(a, b, c):
    """Doubled signed area of triangle (a, b, c); > 0 iff CCW, 0 iff collinear."""
    return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])


def area2(ring):
    """Doubled signed area of a ring (positive iff counter-clockwise)."""
    n = len(ring)
    total = Fraction(0)
    for i in range(n):
        x1, y1 = ring[i]
        x2, y2 = ring[(i + 1) % n]
        total += x1 * y2 - x2 * y1
    return total


def on_segment(a, b, p):
    """True iff p lies on the closed segment a-b (exact)."""
    if orient(a, b, p) != 0:
        return False
    return (
        min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


def _sgn(v):
    return (v > 0) - (v < 0)


def proper_cross(p1, p2, q1, q2):
    """True iff the two closed segments meet in a point interior to both."""
    o1 = orient(q1, q2, p1)
    o2 = orient(q1, q2, p2)
    o3 = orient(p1, p2, q1)
    o4 = orient(p1, p2, q2)
    return _sgn(o1) * _sgn(o2) < 0 and _sgn(o3) * _sgn(o4) < 0


def segments_intersect(p1, p2, q1, q2):
    """True iff the two closed segments share at least one point."""
    if proper_cross(p1, p2, q1, q2):
        return True
    if orient(q1, q2, p1) == 0 and on_segment(q1, q2, p1):
        return True
    if orient(q1, q2, p2) == 0 and on_segment(q1, q2, p2):
        return True
    if orient(p1, p2, q1) == 0 and on_segment(p1, p2, q1):
        return True
    if orient(p1, p2, q2) == 0 and on_segment(p1, p2, q2):
        return True
    return False


def collinear_relation(p, q, a, b):
    """Relation between two collinear segments p-q and a-b.

    Returns one of:
      ("none", None)    - disjoint
      ("point", r)      - they share exactly the single point r
      ("segment", None) - they overlap with positive length
    """
    axis = 0 if p[0] != q[0] else 1
    lo1, hi1 = sorted((p[axis], q[axis]))
    lo2, hi2 = sorted((a[axis], b[axis]))
    lo = max(lo1, lo2)
    hi = min(hi1, hi2)
    if lo > hi:
        return ("none", None)
    if lo == hi:
        for r in (p, q, a, b):
            if r[axis] == lo and on_segment(p, q, r) and on_segment(a, b, r):
                return ("point", r)
        raise AssertionError("collinear touch point not found")
    return ("segment", None)


def classify_point(ring, p):
    """Locate p relative to a (weakly) simple ring: INSIDE / OUTSIDE / BOUNDARY.

    Ray casting towards +x in exact arithmetic; vertices on the ray are
    handled by the half-open ``(ay > y) != (by > y)`` convention, so no
    epsilon is ever needed.  Doubled slit edges (from hole bridges) cancel
    out because they are traversed in opposite directions.
    """
    n = len(ring)
    for i in range(n):
        if on_segment(ring[i], ring[(i + 1) % n], p):
            return BOUNDARY
    inside = False
    x, y = p
    for i in range(n):
        ax, ay = ring[i]
        bx, by = ring[(i + 1) % n]
        if (ay > y) != (by > y):
            xint = ax + (y - ay) * (bx - ax) / (by - ay)
            if xint > x:
                inside = not inside
    return INSIDE if inside else OUTSIDE


def in_triangle_inclusive(p, a, b, c):
    """True iff p is inside or on the CCW triangle (a, b, c)."""
    return (
        orient(a, b, p) >= 0
        and orient(b, c, p) >= 0
        and orient(c, a, p) >= 0
    )


def in_triangle_strict(p, a, b, c):
    """True iff p is strictly inside the CCW triangle (a, b, c)."""
    return orient(a, b, p) > 0 and orient(b, c, p) > 0 and orient(c, a, p) > 0
