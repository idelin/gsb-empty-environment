"""Exact congruence solving.

All rational times are lifted onto a unified tick scale -- the LCM of every
denominator in play -- so the Chinese Remainder Theorem runs on plain Python
integers. Python ints are arbitrary precision, so the unified scale can
neither overflow nor lose precision.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd, lcm


def ceil_div(a: int, b: int) -> int:
    """ceil(a / b) for integers, b > 0."""
    if b <= 0:
        raise ValueError("ceil_div requires a positive divisor")
    return -((-a) // b)


def common_scale(*values: Fraction) -> int:
    """LCM of the denominators of ``values``: the unified exact tick scale."""
    denominators = [Fraction(v).denominator for v in values]
    return lcm(*denominators) if denominators else 1


def crt_pair(r1: int, m1: int, r2: int, m2: int):
    """Solve ``t = r1 (mod m1)`` and ``t = r2 (mod m2)``.

    Returns ``(residue, lcm(m1, m2))`` or ``None`` when the residues disagree
    modulo ``gcd(m1, m2)`` (the arithmetic proof of unsatisfiability).
    """
    g = gcd(m1, m2)
    if (r2 - r1) % g != 0:
        return None
    m1g, m2g = m1 // g, m2 // g
    if m2g == 1:
        x = 0
    else:
        inverse = pow(m1g % m2g, -1, m2g)
        x = ((r2 - r1) // g * inverse) % m2g
    modulus = m1 * m2g
    return (r1 + m1 * x) % modulus, modulus


@dataclass(frozen=True)
class CrtConflict:
    """Arithmetic evidence that a system of congruences has no solution."""

    residue_a: int
    modulus_a: int
    residue_b: int
    modulus_b: int
    divisor: int
    label_a: str
    label_b: str

    def __str__(self) -> str:
        return (
            f"t ≡ {self.residue_a} (mod {self.modulus_a}) [{self.label_a}] conflicts with "
            f"t ≡ {self.residue_b} (mod {self.modulus_b}) [{self.label_b}]: "
            f"residues differ modulo gcd = {self.divisor}"
        )


def crt_all(congruences):
    """Combine ``(residue, modulus, label)`` triples via iterated CRT.

    Returns ``((residue, modulus), None)`` on success, or
    ``(None, CrtConflict)`` describing the first incompatible pair.
    """
    r, m, label = 0, 1, "trivial"
    for residue, modulus, lbl in congruences:
        residue %= modulus
        solved = crt_pair(r, m, residue, modulus)
        if solved is None:
            return None, CrtConflict(r, m, residue, modulus, gcd(m, modulus), label, lbl)
        r, m = solved
        label = lbl if label == "trivial" else f"{label}&{lbl}"
    return (r, m), None
