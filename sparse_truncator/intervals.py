"""Interval arithmetic primitives (standard library only).

Intervals carry the input-box uncertainty through the chain.  Scalar
multiplication flips endpoints for negative coefficients, so sign
information is never lost during propagation.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    """Closed interval [lo, hi] with lo <= hi."""

    lo: float
    hi: float

    def __post_init__(self) -> None:
        if not self.lo <= self.hi:
            raise ValueError(f"empty interval: [{self.lo!r}, {self.hi!r}]")

    def __add__(self, other: "Interval") -> "Interval":
        return Interval(self.lo + other.lo, self.hi + other.hi)

    def scale(self, a: float) -> "Interval":
        """Multiply by a scalar; negative coefficients swap the endpoints."""
        if a >= 0.0:
            return Interval(a * self.lo, a * self.hi)
        return Interval(a * self.hi, a * self.lo)

    def magnitude(self) -> float:
        """max |x| over the interval (worst-case absolute value)."""
        return max(abs(self.lo), abs(self.hi))

    def width(self) -> float:
        return self.hi - self.lo

    def contains(self, x: float, tol: float = 1e-12) -> bool:
        return self.lo - tol <= x <= self.hi + tol
