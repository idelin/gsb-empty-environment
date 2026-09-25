"""Exact planar geometry on rational coordinates.

All coordinates are ``fractions.Fraction`` (ints and strings are converted).
Every predicate is evaluated with exact rational arithmetic; no epsilon
tolerances are used anywhere.  Degenerate cases are defined explicitly:

* A zero-length segment is a point.
* Touching / crossing segments have squared distance exactly 0.
* Collinear overlapping segments have distance 0; the reported parameters
  are the lexicographically smallest ``(t_a, t_b)`` of the overlap set.
* For parallel equidistant configurations (a continuum of closest point
  pairs between one segment pair) the canonical representative is the
  lexicographically smallest ``(dist2, t_a, t_b)`` quadruple.
"""

from fractions import Fraction


def frac(value):
    """Coerce int/str/Fraction to Fraction."""
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


def point(x, y):
    """Build an exact 2D point."""
    return (frac(x), frac(y))


def _sub(a, b):
    return (a[0] - b[0], a[1] - b[1])


def _dot(a, b):
    return a[0] * b[0] + a[1] * b[1]


def _cross(a, b):
    return a[0] * b[1] - a[1] * b[0]


def _pt(p):
    return (frac(p[0]), frac(p[1]))


def orient(a, b, c):
    """Exact orientation sign of the triple (a, b, c): -1, 0 or +1."""
    a, b, c = _pt(a), _pt(b), _pt(c)
    v = _cross(_sub(b, a), _sub(c, a))
    return (v > 0) - (v < 0)


def _on_segment(p, a, b):
    """Exact containment test; caller guarantees collinearity."""
    return (min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1]))


def param_on_segment(p, a, b):
    """Exact parameter t in [0, 1] of point p on segment a->b.

    For a zero-length segment the parameter is defined as 0.
    """
    d = _sub(b, a)
    l2 = _dot(d, d)
    if l2 == 0:
        return Fraction(0)
    return _dot(_sub(p, a), d) / l2


def point_seg_dist2(p, a, b):
    """Squared distance from point p to segment a->b and the parameter.

    Returns ``(dist2, t)`` with exact Fractions.  A zero-length segment
    behaves as a point located at ``a`` with parameter 0.
    """
    p, a, b = _pt(p), _pt(a), _pt(b)
    d = _sub(b, a)
    l2 = _dot(d, d)
    if l2 == 0:
        t = Fraction(0)
    else:
        t = _dot(_sub(p, a), d) / l2
        if t < 0:
            t = Fraction(0)
        elif t > 1:
            t = Fraction(1)
    q = (a[0] + t * d[0], a[1] + t * d[1])
    diff = _sub(p, q)
    return _dot(diff, diff), t


def _intersection_params(a0, a1, b0, b1):
    """Parameters of a common point if the segments touch/cross, else None.

    Returns ``(t_a, t_b)``.  For collinear overlaps the lexicographically
    smallest parameter pair of the overlap set is returned, making the
    result deterministic.
    """
    da = _sub(a1, a0)
    db = _sub(b1, b0)
    la = _dot(da, da)
    lb = _dot(db, db)

    if la == 0 and lb == 0:
        return (Fraction(0), Fraction(0)) if a0 == b0 else None
    if la == 0:
        if orient(b0, b1, a0) == 0 and _on_segment(a0, b0, b1):
            return (Fraction(0), param_on_segment(a0, b0, b1))
        return None
    if lb == 0:
        if orient(a0, a1, b0) == 0 and _on_segment(b0, a0, a1):
            return (param_on_segment(b0, a0, a1), Fraction(0))
        return None

    o1 = orient(a0, a1, b0)
    o2 = orient(a0, a1, b1)
    o3 = orient(b0, b1, a0)
    o4 = orient(b0, b1, a1)

    denom = _cross(da, db)
    if denom != 0:
        if o1 * o2 <= 0 and o3 * o4 <= 0:
            ta = _cross(_sub(b0, a0), db) / denom
            tb = _cross(_sub(b0, a0), da) / denom
            return (ta, tb)
        return None

    if o1 != 0:
        return None  # parallel, strictly separated

    # Collinear: gather every endpoint that lies on the other segment.
    candidates = []
    for p, tp in ((a0, Fraction(0)), (a1, Fraction(1))):
        if _on_segment(p, b0, b1):
            candidates.append((tp, param_on_segment(p, b0, b1)))
    for p, tp in ((b0, Fraction(0)), (b1, Fraction(1))):
        if _on_segment(p, a0, a1):
            candidates.append((param_on_segment(p, a0, a1), tp))
    if not candidates:
        return None
    return min(candidates)


def seg_seg_dist2(a0, a1, b0, b1):
    """Exact squared distance between two segments with parameters.

    Returns ``(dist2, t_a, t_b)`` such that the closest points are
    ``a0 + t_a*(a1-a0)`` and ``b0 + t_b*(b1-b0)``.  Ties (including
    continua from parallel equidistant or overlapping configurations)
    resolve to the lexicographically smallest ``(dist2, t_a, t_b)``.
    """
    a0, a1, b0, b1 = _pt(a0), _pt(a1), _pt(b0), _pt(b1)
    hit = _intersection_params(a0, a1, b0, b1)
    if hit is not None:
        return (Fraction(0), hit[0], hit[1])
    candidates = []
    d2, t = point_seg_dist2(a0, b0, b1)
    candidates.append((d2, Fraction(0), t))
    d2, t = point_seg_dist2(a1, b0, b1)
    candidates.append((d2, Fraction(1), t))
    d2, t = point_seg_dist2(b0, a0, a1)
    candidates.append((d2, t, Fraction(0)))
    d2, t = point_seg_dist2(b1, a0, a1)
    candidates.append((d2, t, Fraction(1)))
    return min(candidates)
