"""Wall-clock time, absolute instants, and elapsed duration -- kept distinct.

The scheduler deliberately never lets an implicit, ambient time zone turn a
calendar expression into an instant.  Conversion always goes through an
explicit :class:`~recur.offsets.OffsetTable`.

All representations are exact integer microseconds (or integral calendar
fields); no floats appear on the time line.
"""

from __future__ import annotations

import datetime as _dt
from dataclasses import dataclass

_USEC_PER_SECOND = 1_000_000
_USEC_PER_DAY = 86_400 * _USEC_PER_SECOND

# An arbitrary, fixed proleptic-Gregorian reference date so wall-clock values
# have a stable ordinal without depending on ``time.localtime``.
_EPOCH_DATE = _dt.date(1970, 1, 1)


def _ymd_to_days(year: int, month: int, day: int) -> int:
    return (_dt.date(year, month, day) - _EPOCH_DATE).days


def _days_to_ymd(days: int) -> tuple[int, int, int]:
    d = _EPOCH_DATE + _dt.timedelta(days=days)
    return d.year, d.month, d.day


@dataclass(frozen=True)
class WallTime:
    """A calendar date plus a time of day, with no offset attached.

    Wall times answer "what the clock says"; they cannot be ordered against
    absolute instants until an offset is supplied.
    """

    year: int
    month: int
    day: int
    microsecond: int

    def __post_init__(self) -> None:
        # Validate the calendar date (rejects month 13, February 31, ...).
        _ymd_to_days(self.year, self.month, self.day)
        if not 0 <= self.microsecond < _USEC_PER_DAY:
            raise ValueError("time of day out of range")

    @classmethod
    def from_parts(
        cls,
        year: int,
        month: int,
        day: int,
        hour: int = 0,
        minute: int = 0,
        second: int = 0,
        microsecond: int = 0,
    ) -> "WallTime":
        tod = (
            hour * 3600 * _USEC_PER_SECOND
            + minute * 60 * _USEC_PER_SECOND
            + second * _USEC_PER_SECOND
            + microsecond
        )
        return cls(year, month, day, tod)

    @classmethod
    def from_datetime(cls, value: _dt.datetime) -> "WallTime":
        if value.tzinfo is not None:
            raise ValueError("datetime must be naive (a pure wall-clock value)")
        return cls.from_parts(
            value.year,
            value.month,
            value.day,
            value.hour,
            value.minute,
            value.second,
            value.microsecond,
        )

    @classmethod
    def from_date(
        cls, value: _dt.date, hour: int = 0, minute: int = 0, second: int = 0
    ) -> "WallTime":
        return cls.from_parts(value.year, value.month, value.day, hour, minute, second)

    @property
    def date(self) -> _dt.date:
        return _dt.date(self.year, self.month, self.day)

    @property
    def hour(self) -> int:
        return (self.microsecond // (3600 * _USEC_PER_SECOND)) % 24

    @property
    def minute(self) -> int:
        return (self.microsecond // (60 * _USEC_PER_SECOND)) % 60

    @property
    def second(self) -> int:
        return (self.microsecond // _USEC_PER_SECOND) % 60

    @property
    def micros(self) -> int:
        return self.microsecond % _USEC_PER_SECOND

    @property
    def epoch_us(self) -> int:
        """Microseconds on the *naive* wall clock since 1970-01-01 00:00.

        This is a convenience for arithmetic and testing; it is not an
        absolute instant.
        """
        return _ymd_to_days(self.year, self.month, self.day) * _USEC_PER_DAY + self.microsecond

    def to_naive_datetime(self) -> _dt.datetime:
        return _dt.datetime(
            self.year,
            self.month,
            self.day,
            self.hour,
            self.minute,
            self.second,
            self.micros,
        )

    def isoformat(self, sep: str = "T") -> str:
        return self.to_naive_datetime().isoformat(sep)

    def add_days(self, days: int) -> "WallTime":
        total = self.epoch_us + days * _USEC_PER_DAY
        d, tod = divmod(total, _USEC_PER_DAY)
        y, m, day = _days_to_ymd(d)
        return WallTime(y, m, day, tod)

    def __sub__(self, other: "WallTime") -> "Duration":
        """Nominal elapsed length between two wall readings.

        This is *not* guaranteed to equal elapsed physical time across offset
        transitions; use instants for that.
        """
        if not isinstance(other, WallTime):
            return NotImplemented
        return Duration(microseconds=self.epoch_us - other.epoch_us)

    def __repr__(self) -> str:
        return f"WallTime({self.isoformat()})"


@dataclass(frozen=True, order=True)
class Instant:
    """An absolute point on the UTC time line, in integer microseconds."""

    epoch_us: int

    @classmethod
    def from_unix(cls, seconds: int | float, microseconds: int = 0) -> "Instant":
        us = int(round(seconds * _USEC_PER_SECOND)) + microseconds
        return cls(us)

    def to_utc_datetime(self) -> _dt.datetime:
        return _dt.datetime(1970, 1, 1) + _dt.timedelta(microseconds=self.epoch_us)

    def isoformat_utc(self) -> str:
        return self.to_utc_datetime().isoformat() + "Z"

    def __add__(self, other: "Duration") -> "Instant":
        if not isinstance(other, Duration):
            return NotImplemented
        return Instant(self.epoch_us + other.microseconds)

    def __sub__(self, other: "Instant | Duration") -> "Instant | Duration":
        if isinstance(other, Instant):
            return Duration(microseconds=self.epoch_us - other.epoch_us)
        if isinstance(other, Duration):
            return Instant(self.epoch_us - other.microseconds)
        return NotImplemented

    def __repr__(self) -> str:
        return f"Instant({self.isoformat_utc()})"


@dataclass(frozen=True, order=True)
class Duration:
    """An exact elapsed amount of physical time, in integer microseconds."""

    microseconds: int = 0

    @classmethod
    def of(
        cls,
        *,
        weeks: int = 0,
        days: int = 0,
        hours: int = 0,
        minutes: int = 0,
        seconds: int = 0,
        microseconds: int = 0,
    ) -> "Duration":
        total = (
            microseconds
            + (
                seconds
                + 60 * minutes
                + 3600 * hours
                + 86_400 * days
                + 7 * 86_400 * weeks
            )
            * _USEC_PER_SECOND
        )
        return cls(total)

    def to_timedelta(self) -> _dt.timedelta:
        return _dt.timedelta(microseconds=self.microseconds)

    def total_seconds(self) -> float:
        return self.microseconds / _USEC_PER_SECOND

    def __add__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.microseconds + other.microseconds)

    def __sub__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.microseconds - other.microseconds)

    def __neg__(self) -> "Duration":
        return Duration(-self.microseconds)
