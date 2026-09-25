"""Exact time model.

Three different concepts are modelled as three different, non-interchangeable
types:

* :class:`WallClock` -- a wall-clock reading in a *named* offset (e.g. "CST").
* :class:`Instant`   -- an exact point on the synthetic timeline (int ticks).
* :class:`Duration`  -- an exact length of time (int ticks).

A :class:`Timeline` owns the *synthetic offset conversion table*: a mapping
from offset names to exact tick offsets.  It is the only place where a
wall-clock reading can be turned into an :class:`Instant`.  All arithmetic is
integer arithmetic -- no floats, no rounding, no ambiguity.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta

TICKS_PER_SECOND = 1_000_000  # one tick == one microsecond, always exact
_EPOCH = datetime(1970, 1, 1)


class TimeModelError(TypeError):
    """Raised when wall-clock times, instants and durations are mixed up."""


def _datetime_to_ticks(dt: datetime) -> int:
    if dt.tzinfo is not None:
        raise TimeModelError("use a naive datetime plus a named offset")
    delta = dt - _EPOCH
    return (delta.days * 86_400 + delta.seconds) * TICKS_PER_SECOND + delta.microseconds


def _ticks_to_datetime(ticks: int) -> datetime:
    return _EPOCH + timedelta(microseconds=ticks)


@dataclass(frozen=True, order=True)
class Duration:
    """An exact length of time.  Not a point in time."""

    ticks: int

    @classmethod
    def of(cls, *, weeks: int = 0, days: int = 0, hours: int = 0,
           minutes: int = 0, seconds: int = 0, microseconds: int = 0) -> "Duration":
        total_days = weeks * 7 + days
        total_seconds = ((total_days * 24 + hours) * 60 + minutes) * 60 + seconds
        return cls(total_seconds * TICKS_PER_SECOND + microseconds)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.ticks + other.ticks)
        if isinstance(other, (Instant, WallClock)):
            raise TimeModelError("add the Duration to the other side instead")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.ticks - other.ticks)
        return NotImplemented

    def __mul__(self, factor: int) -> "Duration":
        if not isinstance(factor, int):
            return NotImplemented
        return Duration(self.ticks * factor)

    __rmul__ = __mul__

    def __neg__(self) -> "Duration":
        return Duration(-self.ticks)

    def __str__(self) -> str:
        sign = "-" if self.ticks < 0 else ""
        ticks = abs(self.ticks)
        seconds, micros = divmod(ticks, TICKS_PER_SECOND)
        days, rem = divmod(seconds, 86_400)
        hours, rem = divmod(rem, 3_600)
        minutes, secs = divmod(rem, 60)
        out = f"{sign}P"
        if days:
            out += f"{days}D"
        out += f"T{hours}H{minutes}M{secs}"
        if micros:
            out += f".{micros:06d}"
        return out + "S"


@dataclass(frozen=True, order=True)
class Instant:
    """An exact point on the synthetic timeline.  Not a wall-clock reading."""

    ticks: int

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.ticks + other.ticks)
        if isinstance(other, (Instant, WallClock)):
            raise TimeModelError("cannot add two points in time")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Instant(self.ticks - other.ticks)
        if isinstance(other, Instant):
            return Duration(self.ticks - other.ticks)
        if isinstance(other, WallClock):
            raise TimeModelError("convert the WallClock to an Instant via a Timeline first")
        return NotImplemented

    def __str__(self) -> str:
        return f"{_ticks_to_datetime(self.ticks).isoformat()}Z"


@dataclass(frozen=True)
class WallClock:
    """A wall-clock reading: local ticks in a named offset.

    Deliberately supports *no* arithmetic: convert to an :class:`Instant`
    through a :class:`Timeline` first, so the offset is always explicit.
    """

    offset: str
    local_ticks: int

    def _no_arithmetic(self, other):
        raise TimeModelError(
            "WallClock supports no arithmetic; convert it to an Instant "
            "through a Timeline (which applies the offset table) first"
        )

    __add__ = _no_arithmetic
    __sub__ = _no_arithmetic

    def __str__(self) -> str:
        return f"{_ticks_to_datetime(self.local_ticks).isoformat()}[{self.offset}]"


class Timeline:
    """The synthetic offset conversion table plus conversions.

    Offsets are stored as exact tick counts: ``local = synthetic + offset``.
    """

    def __init__(self) -> None:
        self._offsets: dict[str, int] = {"UTC": 0}

    def register_offset(self, name: str, offset: Duration) -> None:
        if not isinstance(offset, Duration):
            raise TimeModelError("an offset is a Duration")
        self._offsets[name] = offset.ticks

    def conversion_table(self) -> dict[str, Duration]:
        """A copy of the synthetic offset conversion table."""
        return {name: Duration(ticks) for name, ticks in self._offsets.items()}

    def _offset_ticks(self, name: str) -> int:
        try:
            return self._offsets[name]
        except KeyError:
            raise KeyError(f"unknown offset {name!r}; register it first") from None

    # -- conversions ------------------------------------------------------
    def wall(self, offset: str, dt: datetime) -> WallClock:
        """A wall-clock reading of naive datetime ``dt`` in ``offset``."""
        self._offset_ticks(offset)  # validate the offset exists
        return WallClock(offset, _datetime_to_ticks(dt))

    def instant(self, wc: WallClock) -> Instant:
        """Convert a wall-clock reading to an exact instant."""
        return Instant(wc.local_ticks - self._offset_ticks(wc.offset))

    def at(self, offset: str, dt: datetime) -> Instant:
        """Convenience: wall-clock reading -> instant in one call."""
        return self.instant(self.wall(offset, dt))

    def wallclock(self, instant: Instant, offset: str = "UTC") -> WallClock:
        """Convert an exact instant back to a wall-clock reading."""
        return WallClock(offset, instant.ticks + self._offset_ticks(offset))

    def iso(self, instant: Instant, offset: str = "UTC") -> str:
        return str(self.wallclock(instant, offset))
