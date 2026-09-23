"""2x2 integer matrix arithmetic over Z/mZ.

A matrix [[a, b], [c, d]] is stored as the tuple (a, b, c, d).
Multiplication is non-commutative: multiply(x, y) computes x * y, and in
general multiply(x, y) != multiply(y, x).
"""

from __future__ import annotations

from typing import Sequence

Matrix = tuple[int, int, int, int]

IDENTITY: Matrix = (1, 0, 0, 1)


def normalize(m: Matrix, mod: int) -> Matrix:
    """Reduce every entry of ``m`` modulo ``mod``."""
    a, b, c, d = m
    return (a % mod, b % mod, c % mod, d % mod)


def multiply(x: Matrix, y: Matrix, mod: int) -> Matrix:
    """Return the product ``x * y`` (in that order) modulo ``mod``."""
    a, b, c, d = x
    e, f, g, h = y
    return (
        (a * e + b * g) % mod,
        (a * f + b * h) % mod,
        (c * e + d * g) % mod,
        (c * f + d * h) % mod,
    )


def from_rows(rows: Sequence[Sequence[int]]) -> Matrix:
    """Build a matrix tuple from [[a, b], [c, d]]."""
    (a, b), (c, d) = rows
    return (int(a), int(b), int(c), int(d))


def to_rows(m: Matrix) -> list[list[int]]:
    """Render a matrix tuple as [[a, b], [c, d]]."""
    a, b, c, d = m
    return [[a, b], [c, d]]
