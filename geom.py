"""Exact planar geometry predicates.

All coordinates are ``fractions.Fraction`` (ints and rationals are converted
exactly).  No floating point and no epsilon tolerances are used anywhere:
every predicate is decided by exact sign tests.
"""
from fractions import Fraction

INSIDE = "inside"
OUTSIDE = "outside"
BOUNDARY = "boundary"


def as_point(p):
    """Convert a (x, y) pair of ints/Fractions/strings to exact Fractions."""
    x, y = p
    return (Fraction(x), Fraction(y))


def orient(a, b, c):
    """Exact signed area * 2 of triangle (a, b, c).

    > 0 iff c is strictly left of the directed edge a -> b.
    """
    return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])


def on_segment(a, b, p):
    """True iff p lies on the closed segment ab.  Assumes collinearity."""
    return (min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1]))


def properly_intersect(p1, p2, p3, p4):
    """True iff the open segments p1p2 and p3p4 cross in their interiors."""
    d1 = orient(p3, p4, p1)
    d2 = orient(p3, p4, p2)
    d3 = orient(p1, p2, p3)
    d4 = orient(p1, p2, p4)
    if d1 == 0 or d2 == 0 or d3 == 0 or d4 == 0:
        return False
    return (d1 > 0) != (d2 > 0) and (d3 > 0) != (d4 > 0)


def collinearly_overlap(p1, p2, p3, p4):
    """True iff the segments are collinear and overlap with positive length."""
    if orient(p1, p2, p3) != 0 or orient(p1, p2, p4) != 0:
        return False
    if p1[0] != p2[0]:
        lo1, hi1 = sorted((p1[0], p2[0]))
        lo2, hi2 = sorted((p3[0], p4[0]))
    else:
        lo1, hi1 = sorted((p1[1], p2[1]))
        lo2, hi2 = sorted((p3[1], p4[1]))
    return max(lo1, lo2) < min(hi1, hi2)


def segments_intersect(p1, p2, p3, p4):
    """True iff the closed segments share at least one point."""
    if properly_intersect(p1, p2, p3, p4):
        return True
    if collinearly_overlap(p1, p2, p3, p4):
        return True
    if orient(p3, p4, p1) == 0 and on_segment(p3, p4, p1):
        return True
    if orient(p3, p4, p2) == 0 and on_segment(p3, p4, p2):
        return True
    if orient(p1, p2, p3) == 0 and on_segment(p1, p2, p3):
        return True
    if orient(p1, p2, p4) == 0 and on_segment(p1, p2, p4):
        return True
    return False


def signed_area2(ring):
    """Signed area * 2 of a closed ring (positive iff counter-clockwise)."""
    total = Fraction(0)
    n = len(ring)
    for i in range(n):
        x1, y1 = ring[i]
        x2, y2 = ring[(i + 1) % n]
        total += x1 * y2 - x2 * y1
    return total


def point_in_ring(p, ring):
    """Exact point location: INSIDE, OUTSIDE or BOUNDARY of a closed ring."""
    x, y = p
    inside = False
    n = len(ring)
    for i in range(n):
        a = ring[i]
        b = ring[(i + 1) % n]
        if orient(a, b, p) == 0 and on_segment(a, b, p):
            return BOUNDARY
        # Half-open ray cast to +infinity in x; exact sign test only.
        if (a[1] > y) != (b[1] > y):
            s = orient(a, b, p)
            if (a[1] < b[1] and s > 0) or (a[1] > b[1] and s < 0):
                inside = not inside
    return INSIDE if inside else OUTSIDE


def point_strictly_in_triangle(p, a, b, c):
    """True iff p is strictly inside the (non-degenerate) triangle abc."""
    o = orient(a, b, c)
    if o == 0:
        return False
    s = 1 if o > 0 else -1
    return (orient(a, b, p) * s > 0
            and orient(b, c, p) * s > 0
            and orient(c, a, p) * s > 0)
