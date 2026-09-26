"""Exact time primitives: wall-clock points, logical points and durations.

All values are stored as fractions.Fraction so arithmetic is exact.
Floats are rejected to prevent silent precision loss.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def exact(value) -> Fraction:
    """Coerce an exact numeric (int / str / Fraction) to Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, (int, str)):
        return Fraction(value)
    raise TypeError(
        f"time values must be exact (int, str or Fraction), got {type(value).__name__}"
    )


@dataclass(frozen=True)
class Duration:
    """A length of time; not anchored to any axis."""

    value: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "value", exact(self.value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value - other.value)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)):
            return Duration(self.value * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __neg__(self):
        return Duration(-self.value)

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.value < other.value
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, Duration):
            return self.value <= other.value
        return NotImplemented

    def __str__(self) -> str:
        return f"Duration({self.value})"


class _TimePoint:
    """Shared behaviour for points on a single time axis."""

    value: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "value", exact(self.value))

    def __sub__(self, other):
        if isinstance(other, type(self)):
            return Duration(self.value - other.value)
        if isinstance(other, Duration):
            return type(self)(self.value - other.value)
        return NotImplemented

    def __add__(self, other):
        if isinstance(other, Duration):
            return type(self)(self.value + other.value)
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, type(self)):
            return self.value < other.value
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, type(self)):
            return self.value <= other.value
        return NotImplemented


@dataclass(frozen=True)
class WallClock(_TimePoint):
    """A point on the real (wall-clock) time axis."""

    value: Fraction

    def __str__(self) -> str:
        return f"WallClock({self.value})"


@dataclass(frozen=True)
class LogicalTime(_TimePoint):
    """A point on the logical (media) time axis."""

    value: Fraction

    def __str__(self) -> str:
        return f"LogicalTime({self.value})"
