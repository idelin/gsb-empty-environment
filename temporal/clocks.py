"""Exact time primitives.

Three distinct kinds are modelled and kept type-safe:

- ``WallClockTime``: a reading on a named wall clock (zone + local reading).
  It is *not* an absolute moment; two wall-clock readings cannot be
  subtracted directly.
- ``TimePoint``: an absolute moment, stored as an exact ``Fraction`` of
  seconds since the synthetic epoch.
- ``Duration``: an exact length of time (seconds), the difference of two
  ``TimePoint`` values.

``OffsetTable`` is the synthetic conversion table mapping zone names to
exact offsets: ``reading = epoch_offset + zone_offset``.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Mapping, Union

Number = Union[int, Fraction]


def as_fraction(value: Number) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"time values must be int or Fraction, got {type(value).__name__}")


@dataclass(frozen=True, order=True)
class Duration:
    """An exact length of time. Not a moment."""

    seconds: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "seconds", as_fraction(self.seconds))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __neg__(self) -> "Duration":
        return Duration(-self.seconds)

    def __mul__(self, factor: int) -> "Duration":
        if isinstance(factor, int):
            return Duration(self.seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __repr__(self) -> str:
        return f"Duration({self.seconds}s)"


@dataclass(frozen=True, order=True)
class TimePoint:
    """An absolute moment: exact seconds since the synthetic epoch."""

    offset: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "offset", as_fraction(self.offset))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.offset + other.seconds)
        if isinstance(other, TimePoint):
            raise TypeError("cannot add two TimePoints; subtract them to get a Duration")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self.offset - other.offset)
        if isinstance(other, Duration):
            return TimePoint(self.offset - other.seconds)
        return NotImplemented

    def __repr__(self) -> str:
        return f"TimePoint(epoch+{self.offset}s)"


@dataclass(frozen=True)
class WallClockTime:
    """A reading on a wall clock in a named zone. Not an absolute moment."""

    zone: str
    reading: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "reading", as_fraction(self.reading))

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.zone, self.reading + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.zone, self.reading - other.seconds)
        if isinstance(other, WallClockTime):
            raise TypeError(
                "wall-clock readings cannot be subtracted directly; "
                "convert both with an OffsetTable first"
            )
        return NotImplemented

    def __repr__(self) -> str:
        return f"WallClockTime({self.zone} {self.reading}s)"


class OffsetTable:
    """Synthetic conversion table between named zones and the epoch timeline.

    ``offset[zone]`` is the exact value such that
    ``reading = epoch_offset + offset[zone]``.
    """

    def __init__(self, offsets: Mapping[str, Number]):
        self._offsets = {zone: as_fraction(value) for zone, value in offsets.items()}

    @classmethod
    def synthetic(cls) -> "OffsetTable":
        return cls(
            {
                "UTC": 0,
                "PLUS2": 2 * 3600,
                "MINUS5": -5 * 3600,
                "THIRD": Fraction(1, 3),
            }
        )

    def zones(self) -> list[str]:
        return sorted(self._offsets)

    def offset(self, zone: str) -> Duration:
        try:
            return Duration(self._offsets[zone])
        except KeyError:
            raise KeyError(f"unknown zone {zone!r}; known zones: {self.zones()}") from None

    def to_timepoint(self, wc: WallClockTime) -> TimePoint:
        return TimePoint(wc.reading - self.offset(wc.zone).seconds)

    def to_wallclock(self, tp: TimePoint, zone: str) -> WallClockTime:
        return WallClockTime(zone, tp.offset + self.offset(zone).seconds)

    def duration_between(self, a: WallClockTime, b: WallClockTime) -> Duration:
        """Exact elapsed time from reading ``a`` to reading ``b``."""
        return self.to_timepoint(b) - self.to_timepoint(a)
