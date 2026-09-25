"""Exact rational arithmetic helpers.

All coordinates, mileages and sample parameters in this package are
``fractions.Fraction`` values.  No floating point is used anywhere, so
topological decisions (which segment owns a sample, whether two vertices
coincide, whether a shared edge matches) are exact and never depend on an
epsilon.
"""

from __future__ import annotations

from fractions import Fraction
from math import isqrt
from typing import Tuple, Union

Number = Union[int, Fraction]
Point = Tuple[Fraction, ...]


class NonRationalLengthError(ValueError):
    """Raised when a segment length is not a rational number.

    Mileage is accumulated as exact rationals, therefore every segment
    length must be rational (e.g. axis aligned segments or Pythagorean
    triples such as 3-4-5).  A segment of length sqrt(2) is outside the
    supported domain and fails loudly instead of being silently rounded.
    """


def as_fraction(value: Number) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"coordinate must be int or Fraction, got {type(value).__name__}")


def as_point(coords) -> Point:
    point = tuple(as_fraction(c) for c in coords)
    if len(point) < 2:
        raise ValueError("a point needs at least two coordinates")
    return point


def exact_sqrt(value: Fraction) -> Fraction | None:
    """Return the exact square root of a non-negative Fraction, or None."""
    if value < 0:
        raise ValueError("square root of a negative value")
    num_root = isqrt(value.numerator)
    den_root = isqrt(value.denominator)
    if num_root * num_root == value.numerator and den_root * den_root == value.denominator:
        return Fraction(num_root, den_root)
    return None


def distance(a: Point, b: Point) -> Fraction:
    """Exact Euclidean distance; raises NonRationalLengthError if irrational."""
    if len(a) != len(b):
        raise ValueError("point dimensions do not match")
    squared = sum((x - y) ** 2 for x, y in zip(a, b))
    root = exact_sqrt(Fraction(squared))
    if root is None:
        raise NonRationalLengthError(
            f"segment length sqrt({squared}) is not rational; "
            "rational mileage requires rational segment lengths"
        )
    return root
