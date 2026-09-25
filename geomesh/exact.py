"""Exact 2D geometry predicates over integer / rational coordinates.

Every predicate is computed with ``fractions.Fraction`` arithmetic only.
No floats and no epsilon tolerances appear anywhere, so topological
decisions (on-segment, intersection, orientation) are deterministic and
exactly defined, including all degenerate cases.
"""
from __future__ import annotations

from fractions import Fraction
from typing import Sequence, Union

Number = Union[int, Fraction]
Point = tuple  # (Fraction, Fraction)


def to_fraction(value) -> Fraction:
    """Coerce an int / Fraction / "p/q" string to Fraction.

    Floats (and bools) are rejected outright: accepting them would silently
    introduce inexact coordinates into an exact model.
    """
    if isinstance(value, bool) or isinstance(value, float):
        raise TypeError(
            f"coordinates must be exact (int or Fraction), got {type(value).__name__}"
        )
    return Fraction(value)


def make_point(x, y) -> Point:
    return (to_fraction(x), to_fraction(y))


def orient(a: Point, b: Point, c: Point) -> int:
    """Sign of the cross product (b - a) x (c - a).

    Returns +1 if c is strictly left of a->b, -1 if strictly right,
    0 if exactly collinear. Exact, so 0 really means collinear.
    """
    d = (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])
    return (d > 0) - (d < 0)


def on_segment(a: Point, b: Point, p: Point) -> bool:
    """True iff p is exactly collinear with a-b and inside the bounding box."""
    if orient(a, b, p) != 0:
        return False
    return (
        min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
        and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
    )


def strictly_on_segment(a: Point, b: Point, p: Point) -> bool:
    """True iff p lies on the open segment a-b (endpoints excluded)."""
    return p != a and p != b and on_segment(a, b, p)


def segments_intersect(a: Point, b: Point, c: Point, d: Point) -> bool:
    """True iff closed segments a-b and c-d share at least one point."""
    o1, o2 = orient(a, b, c), orient(a, b, d)
    o3, o4 = orient(c, d, a), orient(c, d, b)
    if o1 == 0 and on_segment(a, b, c):
        return True
    if o2 == 0 and on_segment(a, b, d):
        return True
    if o3 == 0 and on_segment(c, d, a):
        return True
    if o4 == 0 and on_segment(c, d, b):
        return True
    return (o1 != o2) and (o3 != o4)


def segments_overlap(a: Point, b: Point, c: Point, d: Point) -> bool:
    """True iff the segments are collinear and share more than one point."""
    if orient(a, b, c) != 0 or orient(a, b, d) != 0:
        return False
    if a[0] != b[0]:
        lo1, hi1 = sorted((a[0], b[0]))
        lo2, hi2 = sorted((c[0], d[0]))
    else:
        lo1, hi1 = sorted((a[1], b[1]))
        lo2, hi2 = sorted((c[1], d[1]))
    return max(lo1, lo2) < min(hi1, hi2)


def signed_area2(coords: Sequence[Point]) -> Fraction:
    """Twice the signed area of the polygon; sign encodes orientation."""
    total = Fraction(0)
    n = len(coords)
    for i in range(n):
        x1, y1 = coords[i]
        x2, y2 = coords[(i + 1) % n]
        total += x1 * y2 - x2 * y1
    return total


def polygon_is_simple(coords: Sequence[Point]) -> bool:
    """True iff the closed polygonal chain is a simple polygon.

    Defined degenerate behaviour (all exact, no epsilon):
      * fewer than 3 vertices, or any repeated vertex  -> False
      * non-adjacent edges touching or crossing        -> False
      * adjacent edges overlapping collinearly (spike) -> False
      * adjacent edges meeting only at their shared endpoint -> OK
    """
    n = len(coords)
    if n < 3 or len(set(coords)) != n:
        return False
    for i in range(n):
        a, b = coords[i], coords[(i + 1) % n]
        for j in range(i + 1, n):
            c, d = coords[j], coords[(j + 1) % n]
            adjacent = (j == i + 1) or (i == 0 and j == n - 1)
            if adjacent:
                if segments_overlap(a, b, c, d):
                    return False
            elif segments_intersect(a, b, c, d):
                return False
    return True
