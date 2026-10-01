"""Distinct exact types: Duration, TimePoint and WallClockTime.

Everything is stored as ``fractions.Fraction`` seconds (or ticks); no float
is ever introduced, so comparisons and arithmetic on huge epoch values keep
full precision.
"""

from __future__ import annotations

import datetime as _dt
from dataclasses import dataclass
from fractions import Fraction

from .rounding import RoundingPolicy, round_rational
from .units import SECOND, Unit

_UNIX_EPOCH_ORDINAL = _dt.date(1970, 1, 1).toordinal()


@dataclass(frozen=True)
class Duration:
    """An exact length of time (a difference of two time points)."""

    seconds: Fraction

    @classmethod
    def of(cls, value, unit: Unit = SECOND) -> "Duration":
        return cls(unit.ticks_to_seconds(value))

    def to(self, unit: Unit) -> Fraction:
        """Exact (possibly fractional) count of ``unit`` ticks."""
        return unit.seconds_to_ticks(self.seconds)

    def to_int(self, unit: Unit, policy: RoundingPolicy = RoundingPolicy.HALF_EVEN) -> int:
        return round_rational(self.to(unit), policy)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.seconds)

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)):
            return Duration(self.seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, (int, Fraction)):
            return Duration(self.seconds / other)
        if isinstance(other, Duration):
            return self.seconds / other.seconds  # exact ratio, not float
        return NotImplemented

    def __lt__(self, other: "Duration") -> bool:
        return self.seconds < other.seconds

    def __le__(self, other: "Duration") -> bool:
        return self.seconds <= other.seconds


@dataclass(frozen=True)
class TimePoint:
    """An exact instant on a specific clock's timeline."""

    clock: str
    seconds: Fraction  # since that clock's epoch

    @classmethod
    def of(cls, clock: str, value, unit: Unit = SECOND) -> "TimePoint":
        return cls(clock, unit.ticks_to_seconds(value))

    def to(self, unit: Unit) -> Fraction:
        return unit.seconds_to_ticks(self.seconds)

    def to_int(self, unit: Unit, policy: RoundingPolicy = RoundingPolicy.HALF_EVEN) -> int:
        return round_rational(self.to(unit), policy)

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.clock, self.seconds + other.seconds)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.clock, self.seconds - other.seconds)
        if isinstance(other, TimePoint):
            if self.clock != other.clock:
                raise ValueError(
                    f"cannot subtract time points of different clocks: "
                    f"{self.clock!r} vs {other.clock!r}"
                )
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def _check(self, other: "TimePoint"):
        if self.clock != other.clock:
            raise ValueError(
                f"cannot compare different clocks: {self.clock!r} vs {other.clock!r}"
            )

    def __lt__(self, other: "TimePoint") -> bool:
        self._check(other)
        return self.seconds < other.seconds

    def __le__(self, other: "TimePoint") -> bool:
        self._check(other)
        return self.seconds <= other.seconds


@dataclass(frozen=True)
class WallClockTime:
    """An exact civil (UTC) wall-clock instant.

    Stored as exact Fraction seconds since the Unix epoch, so sub-second
    precision is never lost even far from the epoch.
    """

    seconds_since_epoch: Fraction

    @classmethod
    def of(cls, value, unit: Unit = SECOND) -> "WallClockTime":
        return cls(unit.ticks_to_seconds(value))

    @classmethod
    def from_datetime(cls, dt: _dt.datetime, nanosecond: int = 0) -> "WallClockTime":
        """Build from a datetime without going through float timestamps."""
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=_dt.timezone.utc)
        utc = dt.astimezone(_dt.timezone.utc)
        days = utc.date().toordinal() - _UNIX_EPOCH_ORDINAL
        secs = (
            days * 86_400
            + utc.hour * 3_600
            + utc.minute * 60
            + utc.second
        )
        frac = Fraction(utc.microsecond * 1_000 + nanosecond, 1_000_000_000)
        return cls(Fraction(secs) + frac)

    def to(self, unit: Unit) -> Fraction:
        return unit.seconds_to_ticks(self.seconds_since_epoch)

    def to_int(self, unit: Unit, policy: RoundingPolicy = RoundingPolicy.HALF_EVEN) -> int:
        return round_rational(self.to(unit), policy)

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.seconds_since_epoch + other.seconds)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.seconds_since_epoch - other.seconds)
        if isinstance(other, WallClockTime):
            return Duration(self.seconds_since_epoch - other.seconds_since_epoch)
        return NotImplemented

    def __lt__(self, other: "WallClockTime") -> bool:
        return self.seconds_since_epoch < other.seconds_since_epoch

    def __le__(self, other: "WallClockTime") -> bool:
        return self.seconds_since_epoch <= other.seconds_since_epoch

    def iso(self) -> str:
        """Human-readable UTC rendering (exact fractional part appended)."""
        whole = self.seconds_since_epoch.numerator // self.seconds_since_epoch.denominator
        rem = self.seconds_since_epoch - whole
        dt = _dt.datetime(1970, 1, 1, tzinfo=_dt.timezone.utc) + _dt.timedelta(seconds=whole)
        base = dt.strftime("%Y-%m-%dT%H:%M:%S")
        if rem == 0:
            return base + "Z"
        ns = rem * 1_000_000_000
        ns_int = ns.numerator // ns.denominator
        text = f"{base}.{ns_int:09d}".rstrip("0")
        leftover = rem - Fraction(ns_int, 1_000_000_000)
        if leftover != 0:
            text += f"(+{leftover}s)"
        return text + "Z"
