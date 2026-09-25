"""Exact time primitives: wall-clock readings, time points, and durations.

Three distinct concepts are modeled by three distinct, non-interchangeable
types:

* ``WallClock`` -- a civil (wall-clock) reading: calendar date + time of day.
* ``Instant``   -- a single point on the physical timeline, stored exactly as
  an integer number of nanoseconds from the reference-scale epoch.
* ``Duration``  -- an exact length of time, also integer nanoseconds.

``WallClock`` values are converted to and from ``Instant`` values through a
synthetic offset conversion table (:class:`OffsetTable`).  The table lists the
offset in effect from each listed instant onward, which lets tests and demos
model leap-like jumps, skipped wall times, and repeated wall times exactly.
No floats are used anywhere; all arithmetic is exact integer nanoseconds.
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import total_ordering

NANOS_PER_SECOND = 1_000_000_000
NANOS_PER_MINUTE = 60 * NANOS_PER_SECOND
NANOS_PER_HOUR = 60 * NANOS_PER_MINUTE
NANOS_PER_DAY = 24 * NANOS_PER_HOUR

_EPOCH_DAY_OFFSET = 719468  # days from the 0000-03-01 civil epoch to 1970-01-01


def _days_from_civil(year: int, month: int, day: int) -> int:
    """Days since 1970-01-01 for a proleptic Gregorian date (Hinnant's algorithm)."""
    y = year - (1 if month <= 2 else 0)
    era = (y if y >= 0 else y - 399) // 400
    yoe = y - era * 400
    mp = (month + 9) % 12
    doy = (153 * mp + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - _EPOCH_DAY_OFFSET


def _civil_from_days(z: int) -> tuple[int, int, int]:
    """Inverse of :func:`_days_from_civil`."""
    z += _EPOCH_DAY_OFFSET
    era = (z if z >= 0 else z - 146096) // 146097
    doe = z - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    y = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    d = doy - (153 * mp + 2) // 5 + 1
    m = mp + 3 if mp < 10 else mp - 9
    return y + (1 if m <= 2 else 0), m, d


@total_ordering
class Duration:
    """An exact length of time (integer nanoseconds). Not a point in time."""

    __slots__ = ("nanos",)

    def __init__(self, nanos: int = 0):
        self.nanos = int(nanos)

    @classmethod
    def nanoseconds(cls, n: int) -> "Duration":
        return cls(n)

    @classmethod
    def seconds(cls, n: float) -> "Duration":
        return cls(int(n) * NANOS_PER_SECOND)

    @classmethod
    def minutes(cls, n: float) -> "Duration":
        return cls(int(n) * NANOS_PER_MINUTE)

    @classmethod
    def hours(cls, n: float) -> "Duration":
        return cls(int(n) * NANOS_PER_HOUR)

    @classmethod
    def days(cls, n: float) -> "Duration":
        return cls(int(n) * NANOS_PER_DAY)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.nanos + other.nanos)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.nanos - other.nanos)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.nanos)

    def __mul__(self, factor: int):
        if isinstance(factor, int):
            return Duration(self.nanos * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __eq__(self, other):
        if isinstance(other, Duration):
            return self.nanos == other.nanos
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.nanos < other.nanos
        return NotImplemented

    def __hash__(self):
        return hash(("Duration", self.nanos))

    def __repr__(self):
        return f"Duration({self.nanos}ns)"


@total_ordering
class Instant:
    """A single point on the physical timeline (integer nanoseconds from epoch).

    ``Instant - Instant`` yields a :class:`Duration`; ``Instant +/- Duration``
    yields an ``Instant``.  Adding two instants is a type error, as is mixing
    instants with wall-clock readings.
    """

    __slots__ = ("nanos",)

    def __init__(self, nanos: int = 0):
        self.nanos = int(nanos)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.nanos + other.nanos)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Instant(self.nanos - other.nanos)
        if isinstance(other, Instant):
            return Duration(self.nanos - other.nanos)
        return NotImplemented

    def __eq__(self, other):
        if isinstance(other, Instant):
            return self.nanos == other.nanos
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Instant):
            return self.nanos < other.nanos
        return NotImplemented

    def __hash__(self):
        return hash(("Instant", self.nanos))

    def __repr__(self):
        return f"Instant({self.nanos}ns)"


Instant.EPOCH = Instant(0)


@dataclass(frozen=True, order=True)
class WallClock:
    """A civil wall-clock reading. Not a point on the physical timeline until
    converted through an :class:`OffsetTable`."""

    year: int
    month: int
    day: int
    hour: int = 0
    minute: int = 0
    second: int = 0
    nano: int = 0

    def __post_init__(self):
        if not 1 <= self.month <= 12:
            raise ValueError(f"month out of range: {self.month}")
        if not 1 <= self.day <= 31:
            raise ValueError(f"day out of range: {self.day}")
        if not 0 <= self.hour <= 23:
            raise ValueError(f"hour out of range: {self.hour}")
        if not 0 <= self.minute <= 59:
            raise ValueError(f"minute out of range: {self.minute}")
        if not 0 <= self.second <= 59:
            raise ValueError(f"second out of range: {self.second}")
        if not 0 <= self.nano < NANOS_PER_SECOND:
            raise ValueError(f"nano out of range: {self.nano}")

    def _local_nanos(self) -> int:
        days = _days_from_civil(self.year, self.month, self.day)
        return (
            days * NANOS_PER_DAY
            + self.hour * NANOS_PER_HOUR
            + self.minute * NANOS_PER_MINUTE
            + self.second * NANOS_PER_SECOND
            + self.nano
        )


class InvalidWallTimeError(ValueError):
    """The wall-clock reading falls inside a skipped (nonexistent) interval."""


class AmbiguousWallTimeError(ValueError):
    """The wall-clock reading falls inside a repeated interval."""


class OffsetTable:
    """Synthetic offset conversion table.

    ``entries`` are ``(effective_instant, offset)`` pairs: ``offset`` (wall
    minus reference scale) is in effect for all instants >= ``effective``.
    Before the first entry, the first entry's offset applies (extends to
    negative infinity), so conversion is defined on the whole timeline.
    """

    def __init__(self, entries):
        rows = sorted(entries, key=lambda row: row[0].nanos)
        if not rows:
            raise ValueError("offset table must contain at least one entry")
        collapsed = [rows[0]]
        for instant, offset in rows[1:]:
            if offset == collapsed[-1][1]:
                continue
            if instant == collapsed[-1][0]:
                collapsed[-1] = (instant, offset)
                continue
            collapsed.append((instant, offset))
        self._entries = tuple(collapsed)
        self._offsets = tuple(dict.fromkeys(off for _, off in self._entries))

    @property
    def entries(self):
        return self._entries

    def offset_at(self, instant: Instant) -> Duration:
        chosen = self._entries[0][1]
        for effective, offset in self._entries:
            if effective.nanos <= instant.nanos:
                chosen = offset
            else:
                break
        return chosen

    def to_instant(self, wall: WallClock) -> Instant:
        """Convert a wall-clock reading to the matching instant.

        Raises :class:`InvalidWallTimeError` if the reading was skipped by an
        offset jump, or :class:`AmbiguousWallTimeError` if it occurs twice.
        """
        local = wall._local_nanos()
        matches = []
        for offset in self._offsets:
            candidate = Instant(local - offset.nanos)
            if self.offset_at(candidate) == offset and candidate not in matches:
                matches.append(candidate)
        if not matches:
            raise InvalidWallTimeError(f"wall time never occurs: {wall!r}")
        if len(matches) > 1:
            raise AmbiguousWallTimeError(f"wall time occurs twice: {wall!r}")
        return matches[0]

    def to_wall(self, instant: Instant) -> WallClock:
        """Convert an instant back to the wall-clock reading in effect there."""
        local = instant.nanos + self.offset_at(instant).nanos
        days, rem = divmod(local, NANOS_PER_DAY)
        year, month, day = _civil_from_days(days)
        hour, rem = divmod(rem, NANOS_PER_HOUR)
        minute, rem = divmod(rem, NANOS_PER_MINUTE)
        second, nano = divmod(rem, NANOS_PER_SECOND)
        return WallClock(year, month, day, hour, minute, second, nano)
