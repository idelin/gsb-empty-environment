"""Exact planar geometry on rational coordinates.

Every topological predicate (intersection, containment, clearance
comparison) is evaluated with ``fractions.Fraction`` arithmetic.
There is no epsilon anywhere: a comparison such as ``dist2 >= r**2``
is an exact rational comparison, so boundary contact is a well-defined
convention (contact allowed iff ``radius == 0``), never a numerical
accident.
"""

from fractions import Fraction


def to_frac(value):
    """Coerce int / str / Fraction to Fraction. Floats are rejected so
    that binary rounding can never sneak into a topological decision."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, float):
        raise TypeError(
            "float coordinates are not accepted; use int, str or Fraction"
        )
    return Fraction(value)


def to_point(p):
    return (to_frac(p[0]), to_frac(p[1]))


def sub(a, b):
    return (a[0] - b[0], a[1] - b[1])


def dot(a, b):
    return a[0] * b[0] + a[1] * b[1]


def cross(a, b):
    return a[0] * b[1] - a[1] * b[0]


def orient(a, b, c):
    """Exact sign of the oriented area of triangle (a, b, c): +1, 0 or -1."""
    v = cross(sub(b, a), sub(c, a))
    return (v > 0) - (v < 0)


def on_segment(p, a, b):
    """True iff p lies on the closed segment ab (exact)."""
    return cross(sub(b, a), sub(p, a)) == 0 and dot(sub(p, a), sub(p, b)) <= 0


def segments_intersect(a, b, c, d):
    """True iff closed segments ab and cd share at least one point."""
    o1 = orient(a, b, c)
    o2 = orient(a, b, d)
    o3 = orient(c, d, a)
    o4 = orient(c, d, b)
    if o1 * o2 < 0 and o3 * o4 < 0:
        return True
    return (
        (o1 == 0 and on_segment(c, a, b))
        or (o2 == 0 and on_segment(d, a, b))
        or (o3 == 0 and on_segment(a, c, d))
        or (o4 == 0 and on_segment(b, c, d))
    )


def segments_cross_properly(a, b, c, d):
    """True iff the segments intersect transversally (interiors cross)."""
    return (
        orient(a, b, c) * orient(a, b, d) < 0
        and orient(c, d, a) * orient(c, d, b) < 0
    )


def dist2(a, b):
    d = sub(b, a)
    return dot(d, d)


def point_seg_dist2(p, a, b):
    """Exact squared distance from point p to closed segment ab."""
    ab = sub(b, a)
    n = dot(ab, ab)
    if n == 0:  # degenerate segment: a == b
        return dist2(p, a)
    t_num = dot(sub(p, a), ab)
    if t_num <= 0:
        return dist2(p, a)
    if t_num >= n:
        return dist2(p, b)
    c = cross(sub(p, a), ab)
    return c * c / n


def seg_seg_dist2(a, b, c, d):
    """Exact squared distance between two closed segments (2D)."""
    if segments_intersect(a, b, c, d):
        return Fraction(0)
    return min(
        point_seg_dist2(a, c, d),
        point_seg_dist2(b, c, d),
        point_seg_dist2(c, a, b),
        point_seg_dist2(d, a, b),
    )


def polygon_edges(poly):
    return [(poly[i], poly[(i + 1) % len(poly)]) for i in range(len(poly))]


def signed_area2(poly):
    """Twice the signed area; exact. Zero means degenerate polygon."""
    return sum(cross(a, b) for a, b in polygon_edges(poly))


def validate_polygon(poly):
    """Reject degenerate / non-simple polygons with a precise reason.

    Defined degenerate cases (all raise ValueError):
      * fewer than 3 vertices;
      * zero-length edge (consecutive duplicate vertices);
      * zero signed area (e.g. fully collinear vertices);
      * self-intersection or self-touching (non-adjacent edges meeting).
    """
    if len(poly) < 3:
        raise ValueError("polygon needs at least 3 vertices")
    edges = polygon_edges(poly)
    for a, b in edges:
        if a == b:
            raise ValueError("degenerate zero-length edge (duplicate vertex)")
    if signed_area2(poly) == 0:
        raise ValueError("degenerate zero-area polygon")
    n = len(edges)
    for i in range(n):
        for j in range(i + 1, n):
            if j == i + 1 or (i == 0 and j == n - 1):
                continue  # adjacent edges legitimately share one vertex
            if segments_intersect(*edges[i], *edges[j]):
                raise ValueError("self-intersecting or self-touching polygon")


def point_in_polygon(p, poly):
    """Strict-interior test via exact ray casting (+x direction).

    The half-open edge convention (``a.y > y != b.y > y``) makes rays
    through vertices well-defined without any epsilon. Boundary points
    are excluded explicitly (strict interior), never by epsilon.
    """
    if point_on_boundary(p, poly):
        return False
    x, y = p
    inside = False
    for a, b in polygon_edges(poly):
        if (a[1] > y) != (b[1] > y):
            xint = a[0] + (y - a[1]) * (b[0] - a[0]) / (b[1] - a[1])
            if x < xint:
                inside = not inside
    return inside


def point_on_boundary(p, poly):
    return any(on_segment(p, a, b) for a, b in polygon_edges(poly))


def point_polygon_dist2(p, poly):
    """Exact squared distance from p to the closed polygon region.

    Returns 0 when p is inside or on the boundary.
    """
    if point_on_boundary(p, poly) or point_in_polygon(p, poly):
        return Fraction(0)
    return min(point_seg_dist2(p, a, b) for a, b in polygon_edges(poly))


def _segment_boundary_params(p, q, poly):
    """Sorted exact parameter values t in [0,1] where segment pq meets
    the polygon boundary (crossings, touches, collinear overlap ends)."""
    ts = {Fraction(0), Fraction(1)}
    r = sub(q, p)
    rr = dot(r, r)
    for a, b in polygon_edges(poly):
        s = sub(b, a)
        denom = cross(r, s)
        if denom != 0:
            t = cross(sub(a, p), s) / denom
            u = cross(sub(a, p), r) / denom if rr != 0 else Fraction(-1)
            if 0 <= t <= 1 and 0 <= u <= 1:
                ts.add(t)
        elif cross(sub(a, p), r) == 0 and rr != 0:
            # collinear edge: record the overlap interval endpoints
            for v in (a, b):
                t = dot(sub(v, p), r) / rr
                if 0 <= t <= 1:
                    ts.add(t)
    return sorted(ts)


def segment_enters_interior(p, q, poly):
    """Exact test: does closed segment pq contain a strict-interior point?

    Between two consecutive boundary-contact parameters the segment lies
    entirely inside or entirely outside the polygon (it cannot cross the
    boundary there), so sampling the exact midpoint of each interval is a
    complete test -- no epsilon involved.
    """
    if point_in_polygon(p, poly) or point_in_polygon(q, poly):
        return True
    ts = _segment_boundary_params(p, q, poly)
    for t0, t1 in zip(ts, ts[1:]):
        if t0 == t1:
            continue
        tm = (t0 + t1) / 2
        m = (p[0] + tm * (q[0] - p[0]), p[1] + tm * (q[1] - p[1]))
        if point_in_polygon(m, poly):
            return True
    return False


def seg_polygon_dist2(p, q, poly):
    """Exact squared distance from segment pq to the closed polygon region.

    Zero iff the segment touches, crosses or lies inside the polygon.
    """
    if point_polygon_dist2(p, poly) == 0 or point_polygon_dist2(q, poly) == 0:
        return Fraction(0)
    best = None
    for a, b in polygon_edges(poly):
        if segments_intersect(p, q, a, b):
            return Fraction(0)
        d = seg_seg_dist2(p, q, a, b)
        best = d if best is None or d < best else best
    return best
