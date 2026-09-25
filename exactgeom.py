"""Exact planar geometry predicates on rational points.

All points are ``(Fraction, Fraction)`` tuples.  Every predicate is evaluated
with exact rational arithmetic from the standard library; no epsilon-based
comparisons are used anywhere.
"""

from fractions import Fraction


def sign(x):
    return (x > 0) - (x < 0)


def orient(a, b, c):
    """Sign of the cross product (b - a) x (c - a): -1, 0 or +1."""
    return sign((b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0]))


def on_segment(a, b, p):
    """True iff p lies on the closed segment [a, b] (exact)."""
    if orient(a, b, p) != 0:
        return False
    return (min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1]))


def segments_intersect(p1, p2, q1, q2):
    """True iff closed segments [p1,p2] and [q1,q2] share at least one point.

    Proper crossings, endpoint touches and collinear overlaps all count.
    """
    d1 = orient(q1, q2, p1)
    d2 = orient(q1, q2, p2)
    d3 = orient(p1, p2, q1)
    d4 = orient(p1, p2, q2)
    if d1 * d2 < 0 and d3 * d4 < 0:
        return True
    if d1 == 0 and on_segment(q1, q2, p1):
        return True
    if d2 == 0 and on_segment(q1, q2, p2):
        return True
    if d3 == 0 and on_segment(p1, p2, q1):
        return True
    if d4 == 0 and on_segment(p1, p2, q2):
        return True
    return False


def dist2(p, a):
    """Squared Euclidean distance between two points (exact Fraction)."""
    return (p[0] - a[0]) ** 2 + (p[1] - a[1]) ** 2


def point_segment_dist2(p, a, b):
    """Exact squared distance from p to the closed segment [a, b]."""
    dx = b[0] - a[0]
    dy = b[1] - a[1]
    len2 = dx * dx + dy * dy
    if len2 == 0:
        return dist2(p, a)
    t = (p[0] - a[0]) * dx + (p[1] - a[1]) * dy
    if t <= 0:
        return dist2(p, a)
    if t >= len2:
        return dist2(p, b)
    cross = dx * (p[1] - a[1]) - dy * (p[0] - a[0])
    return Fraction(cross * cross, len2)


def point_in_closed_triangle(p, a, b, c):
    """True iff p lies in the closed triangle (a, b, c).

    Degenerate (collinear or repeated-vertex) triangles are handled exactly:
    the "triangle" is then the union of its edges.
    """
    o = orient(a, b, c)
    s1 = orient(a, b, p)
    s2 = orient(b, c, p)
    s3 = orient(c, a, p)
    if o > 0:
        return s1 >= 0 and s2 >= 0 and s3 >= 0
    if o < 0:
        return s1 <= 0 and s2 <= 0 and s3 <= 0
    return on_segment(a, b, p) or on_segment(b, c, p) or on_segment(c, a, p)
