"""Exact 2D geometry on integer / rational coordinates.

Every predicate is evaluated with fractions.Fraction arithmetic, so all
topological decisions (collinearity, point-on-segment, segment intersection,
polygon orientation) are exact.  No epsilon tolerances are used anywhere:
a point is on a line iff the exact cross product is zero.
"""

from fractions import Fraction


def to_fraction(value):
    """Coerce an int or Fraction coordinate to Fraction; reject anything else."""
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid coordinate")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"coordinate must be int or Fraction, got {type(value).__name__}")


class Point:
    """An immutable-ish 2D point with exact rational coordinates."""

    __slots__ = ("x", "y")

    def __init__(self, x, y):
        self.x = to_fraction(x)
        self.y = to_fraction(y)

    def __eq__(self, other):
        return isinstance(other, Point) and self.x == other.x and self.y == other.y

    def __hash__(self):
        return hash((self.x, self.y))

    def __iter__(self):
        yield self.x
        yield self.y

    def __repr__(self):
        return f"Point({self.x}, {self.y})"


def _sign(value):
    if value > 0:
        return 1
    if value < 0:
        return -1
    return 0


def orient(a, b, c):
    """Exact orientation of the triple (a, b, c).

    Returns +1 if c is strictly left of a->b, -1 if strictly right,
    0 iff the three points are exactly collinear.
    """
    cross = (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x)
    return _sign(cross)


def on_segment(a, b, p):
    """True iff p lies on the closed segment ab, given collinearity is exact.

    The caller is expected to have established orient(a, b, p) == 0, or accept
    that a non-collinear p simply fails the bounding-box test.
    """
    return (min(a.x, b.x) <= p.x <= max(a.x, b.x)
            and min(a.y, b.y) <= p.y <= max(a.y, b.y))


def strictly_on_segment(a, b, p):
    """True iff p lies on segment ab and is distinct from both endpoints."""
    return p != a and p != b and orient(a, b, p) == 0 and on_segment(a, b, p)


def segments_intersect(p1, p2, p3, p4):
    """True iff the closed segments p1p2 and p3p4 share at least one point.

    Exact straddle test plus collinear endpoint-touch cases.
    """
    d1 = orient(p3, p4, p1)
    d2 = orient(p3, p4, p2)
    d3 = orient(p1, p2, p3)
    d4 = orient(p1, p2, p4)
    if d1 * d2 < 0 and d3 * d4 < 0:
        return True
    if d1 == 0 and on_segment(p3, p4, p1):
        return True
    if d2 == 0 and on_segment(p3, p4, p2):
        return True
    if d3 == 0 and on_segment(p1, p2, p3):
        return True
    if d4 == 0 and on_segment(p1, p2, p4):
        return True
    return False


def signed_area2(points):
    """Twice the signed area of the polygon; sign gives its orientation."""
    total = Fraction(0)
    n = len(points)
    for i in range(n):
        a = points[i]
        b = points[(i + 1) % n]
        total += a.x * b.y - b.x * a.y
    return total
