"""2x2 integer matrix arithmetic over Z/mZ (row-major, non-commutative)."""

from __future__ import annotations

Matrix = tuple[tuple[int, int], tuple[int, int]]

IDENTITY: Matrix = ((1, 0), (0, 1))


def identity() -> Matrix:
    return IDENTITY


def normalize(entries, mod: int) -> Matrix:
    return (
        (entries[0][0] % mod, entries[0][1] % mod),
        (entries[1][0] % mod, entries[1][1] % mod),
    )


def mul(a: Matrix, b: Matrix, mod: int) -> Matrix:
    return (
        (
            (a[0][0] * b[0][0] + a[0][1] * b[1][0]) % mod,
            (a[0][0] * b[0][1] + a[0][1] * b[1][1]) % mod,
        ),
        (
            (a[1][0] * b[0][0] + a[1][1] * b[1][0]) % mod,
            (a[1][0] * b[0][1] + a[1][1] * b[1][1]) % mod,
        ),
    )


def fold(matrices, mod: int) -> Matrix:
    """Left-to-right ordered product; empty input yields the identity."""
    acc = IDENTITY
    for m in matrices:
        acc = mul(acc, m, mod)
    return acc


def fmt(m: Matrix) -> str:
    return f"[[{m[0][0]}, {m[0][1]}], [{m[1][0]}, {m[1][1]}]]"
