"""Exact 2D affine transforms over integers/rationals (fractions.Fraction).

A Mat2D represents the affine map:
    x' = a*x + c*y + tx
    y' = b*x + d*y + ty

All arithmetic is exact (Fraction). Singularity is decided by det == 0,
which is an exact predicate -- no epsilon is ever involved.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional, Tuple, Union

Number = Union[int, Fraction, str]


def F(value) -> Fraction:
    """Coerce an exact number to Fraction. Floats are rejected on purpose:
    the library guarantees exact rational arithmetic, and a float literal
    would silently smuggle in binary rounding error."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(
        f"non-exact number type {type(value).__name__}; "
        "use int, fractions.Fraction, or a string like '1/2'"
    )


@dataclass(frozen=True)
class Mat2D:
    a: Fraction
    b: Fraction
    c: Fraction
    d: Fraction
    tx: Fraction
    ty: Fraction

    def __matmul__(self, other: "Mat2D") -> "Mat2D":
        """Composition: (self @ other)(p) == self(other(p))."""
        return Mat2D(
            self.a * other.a + self.c * other.b,
            self.b * other.a + self.d * other.b,
            self.a * other.c + self.c * other.d,
            self.b * other.c + self.d * other.d,
            self.a * other.tx + self.c * other.ty + self.tx,
            self.b * other.tx + self.d * other.ty + self.ty,
        )

    @property
    def det(self) -> Fraction:
        return self.a * self.d - self.b * self.c

    @property
    def is_singular(self) -> bool:
        # Exact degenerate test: determinant exactly zero.
        return self.det == 0

    def inverse(self) -> Optional["Mat2D"]:
        """Exact inverse, or None when the matrix is singular.

        There is deliberately no pseudo-inverse fallback: a singular
        transform (e.g. scale by 0) collapses a dimension and has no
        exact inverse, so callers get an explicit non-invertible signal.
        """
        det = self.det
        if det == 0:
            return None
        ia = self.d / det
        ib = -self.b / det
        ic = -self.c / det
        id_ = self.a / det
        itx = -(ia * self.tx + ic * self.ty)
        ity = -(ib * self.tx + id_ * self.ty)
        return Mat2D(ia, ib, ic, id_, itx, ity)

    def apply(self, x, y) -> Tuple[Fraction, Fraction]:
        fx, fy = F(x), F(y)
        return (
            self.a * fx + self.c * fy + self.tx,
            self.b * fx + self.d * fy + self.ty,
        )


def mat(a, b, c, d, tx=0, ty=0) -> Mat2D:
    return Mat2D(F(a), F(b), F(c), F(d), F(tx), F(ty))


IDENTITY = mat(1, 0, 0, 1, 0, 0)


def translation(tx, ty) -> Mat2D:
    return mat(1, 0, 0, 1, tx, ty)


def scaling(sx, sy=None) -> Mat2D:
    if sy is None:
        sy = sx
    return mat(sx, 0, 0, sy, 0, 0)


def rotation(cos, sin) -> Mat2D:
    """Rotation by an angle whose cosine/sine are exact rationals.

    cos^2 + sin^2 == 1 is verified exactly; otherwise the 'rotation'
    would silently be a distortion. Use quarter_turn() for 90-degree
    steps or Pythagorean pairs like (3/5, 4/5).
    """
    fc, fs = F(cos), F(sin)
    if fc * fc + fs * fs != 1:
        raise ValueError(f"not a unit rotation: cos={fc}, sin={fs}")
    return Mat2D(fc, fs, -fs, fc, Fraction(0), Fraction(0))


def quarter_turn(k: int = 1) -> Mat2D:
    """Exact rotation by k * 90 degrees (counter-clockwise)."""
    pairs = [(1, 0), (0, 1), (-1, 0), (0, -1)]
    c, s = pairs[k % 4]
    return rotation(c, s)


def shear(shx, shy=0) -> Mat2D:
    return mat(1, shy, shx, 1, 0, 0)
