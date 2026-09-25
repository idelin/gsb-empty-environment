"""Exact geometry helpers.

Coordinates are integers or rationals only. All predicates are evaluated
exactly with ``fractions.Fraction``; no floating point and no epsilon
tolerances are ever used, so degenerate cases have a precise definition:

- index-degenerate triangle: a vertex index occurs more than once;
- geometrically degenerate (zero-area) triangle: the exact squared
  magnitude of the cross product of two edge vectors is exactly zero.
"""
from __future__ import annotations

from fractions import Fraction


def to_rational(value):
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        f"coordinate must be int or Fraction, got {type(value).__name__}"
    )


def exact_point(coord):
    coord = tuple(coord)
    if len(coord) != 3:
        raise ValueError("only 3D points are supported")
    return tuple(to_rational(c) for c in coord)


def _sub(a, b):
    return tuple(x - y for x, y in zip(a, b))


def _cross(a, b):
    return (
        a[1] * b[2] - a[2] * b[1],
        a[2] * b[0] - a[0] * b[2],
        a[0] * b[1] - a[1] * b[0],
    )


def triangle_double_area_sq(p, q, r):
    """Exact squared magnitude of the cross product (2*area)^2 as a Fraction."""
    cross = _cross(_sub(q, p), _sub(r, p))
    return sum(component * component for component in cross)


def is_zero_area(p, q, r):
    return triangle_double_area_sq(p, q, r) == 0
