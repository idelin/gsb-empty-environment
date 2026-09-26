"""Exact time model.

Three distinct concepts are modeled as three different types so they can
never be silently mixed up:

* ``WallClock`` -- a raw reading taken from one source's local clock.
* ``TimePoint`` -- an absolute point on the reference timeline.
* ``Duration``  -- a signed length of time (difference of two points).

All values are ``fractions.Fraction`` so arithmetic is exact; there is no
floating point anywhere in the pipeline.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Mapping


def _frac(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


@dataclass(frozen=True, order=True, slots=True)
class Duration:
    """A signed length of time."""

    amount: Fraction = Fraction(0)

    def __init__(self, amount=0):
        object.__setattr__(self, "amount", _frac(amount))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.amount + other.amount)
        if isinstance(other, TimePoint):
            return TimePoint(self.amount + other.at)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.amount - other.amount)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.amount)

    def __mul__(self, k):
        if isinstance(k, (int, Fraction)):
            return Duration(self.amount * k)
        return NotImplemented

    __rmul__ = __mul__

    def __repr__(self):
        return f"Duration({self.amount})"


@dataclass(frozen=True, slots=True)
class WallClock:
    """A raw reading from one source's clock. Not comparable across sources."""

    reading: Fraction
    source: str = ""

    def __init__(self, reading, source=""):
        object.__setattr__(self, "reading", _frac(reading))
        object.__setattr__(self, "source", source)

    def __add__(self, other):
        # Applying an offset to a reading yields an absolute time point.
        if isinstance(other, Duration):
            return TimePoint(self.reading + other.amount)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, WallClock):
            if self.source and other.source and self.source != other.source:
                raise TypeError(
                    "cannot subtract wall-clock readings from different sources "
                    f"({self.source!r} vs {other.source!r})"
                )
            return Duration(self.reading - other.reading)
        if isinstance(other, Duration):
            return WallClock(self.reading - other.amount, self.source)
        return NotImplemented

    def __repr__(self):
        return f"WallClock({self.reading}, source={self.source!r})"


@dataclass(frozen=True, order=True, slots=True)
class TimePoint:
    """An absolute point on the reference timeline."""

    at: Fraction

    def __init__(self, at):
        object.__setattr__(self, "at", _frac(at))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.at + other.amount)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self.at - other.at)
        if isinstance(other, Duration):
            return TimePoint(self.at - other.amount)
        return NotImplemented

    def __repr__(self):
        return f"TimePoint({self.at})"


@dataclass(frozen=True, slots=True)
class OffsetInterval:
    """Interval of plausible offsets for one source: true = wall + offset."""

    lo: Duration
    hi: Duration

    def __init__(self, lo, hi):
        lo = lo if isinstance(lo, Duration) else Duration(lo)
        hi = hi if isinstance(hi, Duration) else Duration(hi)
        if lo.amount > hi.amount:
            raise ValueError(f"empty offset interval: [{lo.amount}, {hi.amount}]")
        object.__setattr__(self, "lo", lo)
        object.__setattr__(self, "hi", hi)

    def contains(self, d: Duration) -> bool:
        return self.lo.amount <= d.amount <= self.hi.amount


class OffsetTable:
    """Synthetic offset conversion table: source name -> offset interval.

    Conversion rule: ``true_time = wall_reading + offset`` with
    ``offset`` anywhere inside the source's interval. The table is treated
    as ground truth; it is never adjusted to make contradictory data fit.
    """

    def __init__(self, table: Mapping[str, OffsetInterval]):
        self._table = dict(table)

    def interval(self, source: str) -> OffsetInterval:
        try:
            return self._table[source]
        except KeyError:
            raise ValueError(f"no offset interval registered for source {source!r}") from None

    def convert(self, wall: WallClock) -> tuple[TimePoint, TimePoint]:
        """Earliest and latest plausible true time of a wall-clock reading."""
        iv = self.interval(wall.source)
        return (wall + iv.lo, wall + iv.hi)

    def sources(self) -> tuple[str, ...]:
        return tuple(self._table)

    def __repr__(self):
        body = ", ".join(
            f"{name}: [{iv.lo.amount}, {iv.hi.amount}]" for name, iv in self._table.items()
        )
        return f"OffsetTable({body})"
