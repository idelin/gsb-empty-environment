"""Exact 2D geometry primitives.

All coordinates are exact rationals (``fractions.Fraction``).  No floating
point and no epsilon tolerances are used anywhere in this package: every
topological decision is made with exact arithmetic plus a fixed, documented
tie-breaking convention (see ``scanfill.scanline``).
"""
from __future__ import annotations

from fractions import Fraction
from typing import NamedTuple, Tuple, Union

#: A coordinate may be given as an int, a Fraction, or a (num, den) pair.
Number = Union[int, Fraction, Tuple[int, int]]


def to_frac(value) -> Fraction:
    """Coerce an exact numeric value to ``Fraction``; floats are rejected."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("booleans are not coordinates")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, (tuple, list)) and len(value) == 2:
        num, den = value
        if isinstance(num, int) and isinstance(den, int):
            return Fraction(num, den)
    raise TypeError(
        f"unsupported coordinate {value!r}: use int, Fraction or (num, den)"
    )


class Point(NamedTuple):
    x: Fraction
    y: Fraction


def point(x, y) -> Point:
    return Point(to_frac(x), to_frac(y))


class Transform:
    """Exact 2D affine transform.

        x' = a*x + b*y + c
        y' = d*x + e*y + f

    All six coefficients are exact rationals.  A transform whose
    determinant is zero is *singular*: it collapses the plane onto a
    line (or a point).  Applying a singular transform is well defined;
    the resulting degenerate polygon simply covers no pixel centres.
    """

    __slots__ = ("a", "b", "c", "d", "e", "f")

    def __init__(self, a=1, b=0, c=0, d=0, e=1, f=0):
        self.a = to_frac(a)
        self.b = to_frac(b)
        self.c = to_frac(c)
        self.d = to_frac(d)
        self.e = to_frac(e)
        self.f = to_frac(f)

    @classmethod
    def identity(cls) -> "Transform":
        return cls()

    @classmethod
    def translate(cls, tx, ty) -> "Transform":
        return cls(1, 0, tx, 0, 1, ty)

    @classmethod
    def scale(cls, sx, sy=None) -> "Transform":
        if sy is None:
            sy = sx
        return cls(sx, 0, 0, 0, sy, 0)

    def determinant(self) -> Fraction:
        return self.a * self.e - self.b * self.d

    def is_singular(self) -> bool:
        return self.determinant() == 0

    def then(self, other: "Transform") -> "Transform":
        """Compose: the result applies ``self`` first, then ``other``."""
        return Transform(
            other.a * self.a + other.b * self.d,
            other.a * self.b + other.b * self.e,
            other.a * self.c + other.b * self.f + other.c,
            other.d * self.a + other.e * self.d,
            other.d * self.b + other.e * self.e,
            other.d * self.c + other.e * self.f + other.f,
        )

    def apply(self, p: Point) -> Point:
        return Point(
            self.a * p.x + self.b * p.y + self.c,
            self.d * p.x + self.e * p.y + self.f,
        )

    def __repr__(self) -> str:
        return (
            f"Transform(a={self.a}, b={self.b}, c={self.c}, "
            f"d={self.d}, e={self.e}, f={self.f})"
        )
