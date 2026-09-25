"""Exact time model.

Three distinct concepts are kept as separate, non-interchangeable types:

- ``TimePoint``: an absolute position on the tick timeline (exact ``Fraction``).
- ``WallClockTime``: an absolute wall-clock moment (exact seconds since an epoch).
- ``Duration``: a length of time in a declared unit (``tick`` or ``second``).

Conversion between the tick timeline and the wall clock goes through a
synthetic :class:`OffsetTable` -- a piecewise-linear, exactly invertible
conversion table with rational rates.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from fractions import Fraction
from functools import total_ordering

TICK = "tick"
SECOND = "second"

_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


@dataclass(frozen=True)
class Duration:
    """An exact length of time in a declared unit."""

    amount: Fraction
    unit: str = TICK

    def __post_init__(self) -> None:
        object.__setattr__(self, "amount", Fraction(self.amount))
        if self.unit not in (TICK, SECOND):
            raise ValueError(f"unknown duration unit: {self.unit!r}")

    def _coerce(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        if self.unit != other.unit:
            raise TypeError(
                f"cannot combine a {self.unit} duration with a {other.unit} duration"
            )
        return other

    def __add__(self, other):
        other = self._coerce(other)
        if other is NotImplemented:
            return NotImplemented
        return Duration(self.amount + other.amount, self.unit)

    def __sub__(self, other):
        other = self._coerce(other)
        if other is NotImplemented:
            return NotImplemented
        return Duration(self.amount - other.amount, self.unit)

    def __neg__(self):
        return Duration(-self.amount, self.unit)

    def __mul__(self, factor):
        return Duration(self.amount * Fraction(factor), self.unit)

    __rmul__ = __mul__


@total_ordering
@dataclass(frozen=True)
class TimePoint:
    """An absolute position on the tick timeline."""

    tick: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "tick", Fraction(self.tick))

    def __add__(self, duration):
        if not isinstance(duration, Duration):
            return NotImplemented
        if duration.unit != TICK:
            raise TypeError("only tick durations can shift a TimePoint")
        return TimePoint(self.tick + duration.amount)

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self.tick - other.tick, TICK)
        if isinstance(other, Duration):
            if other.unit != TICK:
                raise TypeError("only tick durations can shift a TimePoint")
            return TimePoint(self.tick - other.amount)
        return NotImplemented

    def __lt__(self, other):
        if not isinstance(other, TimePoint):
            return NotImplemented
        return self.tick < other.tick


@total_ordering
@dataclass(frozen=True)
class WallClockTime:
    """An absolute wall-clock moment, stored as exact seconds since ``epoch``."""

    seconds: Fraction
    epoch: datetime = _EPOCH

    def __post_init__(self) -> None:
        object.__setattr__(self, "seconds", Fraction(self.seconds))

    def to_datetime(self) -> datetime:
        """Nearest representable datetime (microsecond rounding, display only).

        The exact value always remains available as ``self.seconds``.
        """
        return self.epoch + timedelta(seconds=float(self.seconds))

    def _check_epoch(self, other) -> None:
        if self.epoch != other.epoch:
            raise TypeError("wall-clock times with different epochs are incomparable")

    def __add__(self, duration):
        if not isinstance(duration, Duration):
            return NotImplemented
        if duration.unit != SECOND:
            raise TypeError("only second durations can shift a WallClockTime")
        return WallClockTime(self.seconds + duration.amount, self.epoch)

    def __sub__(self, other):
        if isinstance(other, WallClockTime):
            self._check_epoch(other)
            return Duration(self.seconds - other.seconds, SECOND)
        if isinstance(other, Duration):
            if other.unit != SECOND:
                raise TypeError("only second durations can shift a WallClockTime")
            return WallClockTime(self.seconds - other.amount, self.epoch)
        return NotImplemented

    def __lt__(self, other):
        if not isinstance(other, WallClockTime):
            return NotImplemented
        self._check_epoch(other)
        return self.seconds < other.seconds


@dataclass(frozen=True)
class OffsetEntry:
    """One row of the synthetic offset table.

    For ticks in ``[tick_start, next_row.tick_start)`` the wall clock advances
    at ``rate`` wall-seconds per tick, anchored so that tick ``tick_start``
    maps to ``wall_start`` seconds since the epoch.
    """

    tick_start: Fraction
    wall_start: Fraction
    rate: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "tick_start", Fraction(self.tick_start))
        object.__setattr__(self, "wall_start", Fraction(self.wall_start))
        object.__setattr__(self, "rate", Fraction(self.rate))
        if self.rate <= 0:
            raise ValueError("offset table rate must be positive")


class OffsetTable:
    """Synthetic, continuous, piecewise-linear tick <-> wall-clock table.

    Every conversion is exact rational arithmetic; the table must be
    continuous so that both directions are well defined.
    """

    def __init__(self, entries, epoch: datetime = _EPOCH):
        rows = sorted(
            (e if isinstance(e, OffsetEntry) else OffsetEntry(*e) for e in entries),
            key=lambda entry: entry.tick_start,
        )
        if not rows:
            raise ValueError("offset table needs at least one entry")
        for prev, nxt in zip(rows, rows[1:]):
            if nxt.tick_start <= prev.tick_start:
                raise ValueError("offset table tick starts must be strictly increasing")
            expected = prev.wall_start + (nxt.tick_start - prev.tick_start) * prev.rate
            if expected != nxt.wall_start:
                raise ValueError(
                    f"discontinuous offset table at tick {nxt.tick_start}: "
                    f"expected wall {expected}, found {nxt.wall_start}"
                )
        self._rows = tuple(rows)
        self.epoch = epoch

    def _row_for_tick(self, tick: Fraction) -> OffsetEntry:
        if tick < self._rows[0].tick_start:
            raise ValueError(f"tick {tick} is before the start of the offset table")
        row = self._rows[0]
        for candidate in self._rows:
            if candidate.tick_start <= tick:
                row = candidate
            else:
                break
        return row

    def _row_for_wall(self, seconds: Fraction) -> OffsetEntry:
        if seconds < self._rows[0].wall_start:
            raise ValueError(
                f"wall second {seconds} is before the start of the offset table"
            )
        row = self._rows[0]
        for candidate in self._rows:
            if candidate.wall_start <= seconds:
                row = candidate
            else:
                break
        return row

    def to_wall(self, point: TimePoint) -> WallClockTime:
        row = self._row_for_tick(point.tick)
        seconds = row.wall_start + (point.tick - row.tick_start) * row.rate
        return WallClockTime(seconds, self.epoch)

    def to_timepoint(self, moment: WallClockTime) -> TimePoint:
        if moment.epoch != self.epoch:
            raise TypeError("wall-clock time uses a different epoch than this table")
        row = self._row_for_wall(moment.seconds)
        tick = row.tick_start + (moment.seconds - row.wall_start) / row.rate
        return TimePoint(tick)
