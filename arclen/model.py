"""Exact rational geometry model for polylines.

Coordinates are ints or Fractions only (floats are rejected so no silent
rounding can sneak in).  Segment lengths must be rational; otherwise
NonRationalLengthError is raised.  That is a defined contract, not an
epsilon-based patch: every topological decision (zero-length, breaks,
point equality, canonical edge order) is made with exact arithmetic.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Union

Number = Union[int, Fraction]
Point = tuple  # tuple[Fraction, ...]


class NonRationalLengthError(ValueError):
    """A segment's Euclidean length is not a rational number."""


def _to_fraction(value: Number) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(
            f"coordinates must be int or Fraction, got {type(value).__name__}"
        )
    return Fraction(value)


def to_point(coords: Iterable[Number]) -> Point:
    point = tuple(_to_fraction(c) for c in coords)
    if not point:
        raise ValueError("a point needs at least one coordinate")
    return point


def rational_sqrt(value: Fraction) -> Fraction | None:
    """Exact square root of a non-negative Fraction, or None if irrational."""
    if value < 0:
        return None
    num = math.isqrt(value.numerator)
    den = math.isqrt(value.denominator)
    if num * num == value.numerator and den * den == value.denominator:
        return Fraction(num, den)
    return None


def segment_length(a: Point, b: Point) -> Fraction:
    """Exact rational length of a segment; raises NonRationalLengthError."""
    if len(a) != len(b):
        raise ValueError(f"dimension mismatch: {len(a)} vs {len(b)}")
    squared = sum((bi - ai) ** 2 for ai, bi in zip(a, b))
    root = rational_sqrt(squared)
    if root is None:
        raise NonRationalLengthError(
            f"segment {a} -> {b} has irrational length (squared={squared})"
        )
    return root


@dataclass(frozen=True)
class Vertex:
    """A polyline vertex.

    key:          anchor point - always emitted as a sample and resets the
                  local sampling grid (this is what makes edits local).
    break_before: topological break - no segment connects the previous
                  vertex to this one, so nothing is ever interpolated
                  across it.
    """

    point: Point
    key: bool = False
    break_before: bool = False

    def __post_init__(self):
        object.__setattr__(self, "point", to_point(self.point))


@dataclass(frozen=True)
class Polyline:
    """An open or closed chain of vertices.

    Segment j goes from vertex j to vertex (j+1) % n and exists unless the
    destination vertex has break_before.  For an open polyline the last
    vertex has no outgoing segment.  ``shared`` holds indices of segments
    that are shared edges (sampled canonically via a SeamRegistry).
    """

    vertices: tuple
    closed: bool = False
    shared: frozenset = frozenset()

    def __post_init__(self):
        verts = tuple(v if isinstance(v, Vertex) else Vertex(v) for v in self.vertices)
        object.__setattr__(self, "vertices", verts)
        object.__setattr__(self, "shared", frozenset(self.shared))

    def segment_exists(self, j: int) -> bool:
        n = len(self.vertices)
        if not 0 <= j < n:
            return False
        if j == n - 1:
            return self.closed and not self.vertices[0].break_before
        return not self.vertices[j + 1].break_before
