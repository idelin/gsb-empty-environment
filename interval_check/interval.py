"""Closed integer intervals and conservative interval arithmetic.

Intervals are *integer* intervals ``[lo, hi]`` with ``lo <= hi``.
Every operation over-approximates the true value set: any exact result is
guaranteed to lie in the computed interval.  The price is loss of variable
correlation (e.g. ``x - x`` is reported as a wider interval than the exact
``[0, 0]``); such widening must never be treated as a real defect.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    """A closed interval of consecutive integers ``[lo, hi]``."""

    lo: int
    hi: int

    def __post_init__(self) -> None:
        if isinstance(self.lo, bool) or isinstance(self.hi, bool):
            raise TypeError("interval bounds must be integers")
        if not isinstance(self.lo, int) or not isinstance(self.hi, int):
            raise TypeError("interval bounds must be integers")
        if self.lo > self.hi:
            raise ValueError(f"empty interval: [{self.lo}, {self.hi}]")

    @property
    def width(self) -> int:
        """Number of integer points in the interval."""
        return self.hi - self.lo + 1

    @property
    def contains_zero(self) -> bool:
        return self.lo <= 0 <= self.hi

    def contains(self, value: int) -> bool:
        return self.lo <= value <= self.hi

    def negate(self) -> "Interval":
        return Interval(-self.hi, -self.lo)

    def add(self, other: "Interval") -> "Interval":
        return Interval(self.lo + other.lo, self.hi + other.hi)

    def sub(self, other: "Interval") -> "Interval":
        return Interval(self.lo - other.hi, self.hi - other.lo)

    def mul(self, other: "Interval") -> "Interval":
        # A bilinear product over a box attains its extrema at corners.
        candidates = (
            self.lo * other.lo,
            self.lo * other.hi,
            self.hi * other.lo,
            self.hi * other.hi,
        )
        return Interval(min(candidates), max(candidates))

    def floor_div(self, other: "Interval") -> "Interval":
        """Conservative floor (Euclidean, ``//``) division.

        ``other`` must not contain zero.  On the rectangle of divisor and
        dividend points every corner is a candidate: for a fixed divisor the
        function is monotone in the dividend, and for a fixed dividend it is
        monotone in the divisor on each side of zero.
        """
        if other.contains_zero:
            raise ZeroDivisionError("divisor interval contains zero")
        candidates = tuple(
            dividend // divisor
            for dividend in (self.lo, self.hi)
            for divisor in (other.lo, other.hi)
        )
        return Interval(min(candidates), max(candidates))

    def __str__(self) -> str:
        return f"[{self.lo}, {self.hi}]"
