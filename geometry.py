"""Exact computational-geometry primitives on rational coordinates.

Every point is a ``(Fraction, Fraction)`` tuple.  All predicates and
distances are computed with exact integer/rational arithmetic, so
topological decisions (inside/outside, intersecting/disjoint, clear/blocked)
never depend on a floating-point epsilon.

Conventions
-----------
* An obstacle "shape" is a list of 1..N vertices:
    - 1 vertex  -> a point obstacle (its dilation is a disk);
    - 2 vertices -> a segment obstacle (its dilation is a capsule);
    - >=3 vertices -> a filled polygon; the boundary belongs to the obstacle.
* Self-intersecting polygons are filled with the even-odd rule.
* All distances are returned as exact squared distances (Fraction).
"""

from fractions import Fraction


def as_point(p):
    """Coerce a 2-sequence of int/Fraction into an exact point."""
    return (Fraction(p[0]), Fraction(p[1]))


def orient(a, b, c):
    """Exact twice-signed-area of triangle (a, b, c).

    > 0 if c lies strictly to the left of the directed line a -> b,
    < 0 to the right, == 0 iff the three points are collinear.
    """
    return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])


def _sign(x):
    return (x > 0) - (x < 0)


def on_segment(a, b, p):
    """True iff p lies on the closed segment ab (exact)."""
    if orient(a, b, p) != 0:
        return False
    return (min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1]))


def segments_intersect(a, b, c, d):
    """True iff the closed segments ab and cd share at least one point."""
    o1, o2 = orient(a, b, c), orient(a, b, d)
    o3, o4 = orient(c, d, a), orient(c, d, b)
    if _sign(o1) * _sign(o2) < 0 and _sign(o3) * _sign(o4) < 0:
        return True
    if o1 == 0 and on_segment(a, b, c):
        return True
    if o2 == 0 and on_segment(a, b, d):
        return True
    if o3 == 0 and on_segment(c, d, a):
        return True
    if o4 == 0 and on_segment(c, d, b):
        return True
    return False


def dist2(p, q):
    """Exact squared Euclidean distance between two points."""
    return (p[0] - q[0]) ** 2 + (p[1] - q[1]) ** 2


def point_seg_dist2(p, a, b):
    """Exact squared distance from point p to the closed segment ab."""
    dx, dy = b[0] - a[0], b[1] - a[1]
    if dx == 0 and dy == 0:
        return dist2(p, a)
    t = ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / (dx * dx + dy * dy)
    if t <= 0:
        return dist2(p, a)
    if t >= 1:
        return dist2(p, b)
    proj = (a[0] + t * dx, a[1] + t * dy)
    return dist2(p, proj)


def seg_seg_dist2(a, b, c, d):
    """Exact squared distance between the closed segments ab and cd."""
    if segments_intersect(a, b, c, d):
        return Fraction(0)
    return min(
        point_seg_dist2(a, c, d),
        point_seg_dist2(b, c, d),
        point_seg_dist2(c, a, b),
        point_seg_dist2(d, a, b),
    )


def shape_edges(poly):
    """Boundary edges of a shape: none for a point, one for a segment."""
    n = len(poly)
    if n < 2:
        return []
    if n == 2:
        return [(poly[0], poly[1])]
    return [(poly[i], poly[(i + 1) % n]) for i in range(n)]


def point_in_polygon(p, poly):
    """True iff p lies inside or on the boundary of ``poly`` (closed set).

    Fill rule: even-odd.  The boundary belongs to the polygon.
    Exact ray casting with a half-open y-interval convention, so vertices
    lying exactly on the ray are resolved deterministically (no epsilon).
    """
    for a, b in shape_edges(poly):
        if on_segment(a, b, p):
            return True
    inside = False
    n = len(poly)
    for i in range(n):
        a, b = poly[i], poly[(i + 1) % n]
        if (a[1] <= p[1] < b[1]) or (b[1] <= p[1] < a[1]):
            x_cross = a[0] + (p[1] - a[1]) * (b[0] - a[0]) / (b[1] - a[1])
            if x_cross > p[0]:
                inside = not inside
    return inside


def dist2_point_shape(p, poly):
    """Exact squared distance from a point to a point/segment/polygon."""
    if len(poly) == 1:
        return dist2(p, poly[0])
    if len(poly) == 2:
        return point_seg_dist2(p, poly[0], poly[1])
    if point_in_polygon(p, poly):
        return Fraction(0)
    return min(point_seg_dist2(p, a, b) for a, b in shape_edges(poly))


def dist2_seg_shape(p, q, poly):
    """Exact squared distance from the closed segment pq to a shape."""
    if len(poly) == 1:
        return point_seg_dist2(poly[0], p, q)
    if len(poly) == 2:
        return seg_seg_dist2(p, q, poly[0], poly[1])
    if point_in_polygon(p, poly) or point_in_polygon(q, poly):
        return Fraction(0)
    best = None
    for a, b in shape_edges(poly):
        d2 = seg_seg_dist2(p, q, a, b)
        if best is None or d2 < best:
            best = d2
    return best


def dist2_cell_shape(corners, poly):
    """Exact squared distance between a closed grid cell and a shape.

    ``corners`` is the list of 4 cell corners (CCW).  Returns 0 iff the
    closed cell and the (closed) shape touch or overlap.
    """
    cell_edges = shape_edges(corners)
    pedges = shape_edges(poly)
    # Cell corner inside polygon, or any shape vertex inside the cell.
    if len(poly) >= 3 and point_in_polygon(corners[0], poly):
        return Fraction(0)
    if point_in_polygon(poly[0], corners):
        return Fraction(0)
    best = None
    for c1, c2 in cell_edges:
        if pedges:
            for p1, p2 in pedges:
                d2 = seg_seg_dist2(c1, c2, p1, p2)
                if best is None or d2 < best:
                    best = d2
        else:  # point obstacle outside the cell
            d2 = point_seg_dist2(poly[0], c1, c2)
            if best is None or d2 < best:
                best = d2
    return best
