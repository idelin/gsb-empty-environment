"""Effective-time computation over daily open windows and closed exceptions.

A :class:`Schedule` combines:

- daily open windows given in *wall-clock* time (e.g. 01:00-04:00 every day);
- exception closed intervals given as absolute instants.

All arithmetic is done on :class:`~timepoint.Instant` values, so crossing an
offset transition is automatically counted by real elapsed time: a 01:00-04:00
window on a spring-forward day really lasts 2 hours.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, time, timedelta

from timepoint import Duration, Instant, OffsetTable, WallTime


@dataclass(frozen=True)
class DailyWindow:
    """A daily open window in wall-clock time; overnight windows allowed."""

    opens: time
    closes: time


@dataclass(frozen=True)
class ClosedInterval:
    """A half-open exception interval [start, end) during which time is closed."""

    start: Instant
    end: Instant

    def __post_init__(self) -> None:
        if not isinstance(self.start, Instant) or not isinstance(self.end, Instant):
            raise TypeError("ClosedInterval bounds must be Instant values")
        if not self.start < self.end:
            raise ValueError("ClosedInterval requires start < end")

    @classmethod
    def from_wall(cls, table: OffsetTable, start: WallTime, end: WallTime) -> "ClosedInterval":
        return cls(table.to_instant(start), table.to_instant(end))


def _merge(intervals: list[tuple[Instant, Instant]]) -> list[tuple[Instant, Instant]]:
    """Merge overlapping or touching intervals so overlaps count only once."""
    merged: list[list[Instant]] = []
    for start, end in sorted(intervals):
        if merged and start <= merged[-1][1]:
            if end > merged[-1][1]:
                merged[-1][1] = end
        else:
            merged.append([start, end])
    return [(start, end) for start, end in merged]


def _subtract(segments: list[tuple[Instant, Instant]],
              cuts: list[tuple[Instant, Instant]]) -> list[tuple[Instant, Instant]]:
    result: list[tuple[Instant, Instant]] = []
    for seg_start, seg_end in segments:
        pieces = [(seg_start, seg_end)]
        for cut_start, cut_end in cuts:
            next_pieces = []
            for piece_start, piece_end in pieces:
                if cut_end <= piece_start or cut_start >= piece_end:
                    next_pieces.append((piece_start, piece_end))
                    continue
                if piece_start < cut_start:
                    next_pieces.append((piece_start, cut_start))
                if cut_end < piece_end:
                    next_pieces.append((cut_end, piece_end))
            pieces = next_pieces
        result.extend(pieces)
    return result


class Schedule:
    def __init__(self, table: OffsetTable, windows: list[DailyWindow],
                 closed: list[ClosedInterval] | None = None) -> None:
        self._table = table
        self._windows = list(windows)
        self._closed = _merge([(c.start, c.end) for c in (closed or [])])

    @property
    def table(self) -> OffsetTable:
        return self._table

    @property
    def closed(self) -> list[tuple[Instant, Instant]]:
        return list(self._closed)

    def open_segments(self, day: date) -> list[tuple[Instant, Instant]]:
        """Effective open segments (half-open instant intervals) for a wall date."""
        raw: list[tuple[Instant, Instant]] = []
        for window in self._windows:
            open_wall = WallTime(datetime.combine(day, window.opens))
            close_day = day if window.closes > window.opens else day + timedelta(days=1)
            close_wall = WallTime(datetime.combine(close_day, window.closes))
            start = self._table.to_instant(open_wall)
            end = self._table.to_instant(close_wall)
            if end > start:
                raw.append((start, end))
        return _subtract(_merge(raw), self._closed)

    def _jump_out_of_closed(self, instant: Instant) -> Instant:
        """If ``instant`` falls inside a closed interval, move to its end."""
        moved = instant
        for cut_start, cut_end in self._closed:
            if cut_start <= moved < cut_end:
                moved = cut_end
        return moved


def _require_instant(value: Instant, name: str) -> None:
    if not isinstance(value, Instant):
        raise TypeError(f"{name} must be an Instant, got {type(value).__name__}")


def effective_duration(schedule: Schedule, start: Instant, end: Instant) -> Duration:
    """Effective (open, not closed) duration between two instants."""
    _require_instant(start, "start")
    _require_instant(end, "end")
    if end < start:
        raise ValueError("end must not be before start")
    total = 0
    table = schedule.table
    day = table.to_wall(start).naive.date()
    last_day = table.to_wall(end).naive.date()
    while day <= last_day:
        for seg_start, seg_end in schedule.open_segments(day):
            low = max(seg_start, start)
            high = min(seg_end, end)
            if high > low:
                total += (high - low).seconds
        day += timedelta(days=1)
    return Duration(total)


def add_effective_duration(schedule: Schedule, start: Instant, delta: Duration) -> Instant:
    """Add an effective duration to ``start`` and return the resulting instant.

    Only open, non-closed time is consumed.  If the result would land inside a
    closed interval, it jumps forward to the next open segment.
    """
    _require_instant(start, "start")
    if not isinstance(delta, Duration):
        raise TypeError(f"delta must be a Duration, got {type(delta).__name__}")
    if delta.seconds < 0:
        raise ValueError("delta must not be negative")
    if delta.seconds == 0:
        return start
    remaining = delta.seconds
    day = schedule.table.to_wall(start).naive.date()
    while True:
        for seg_start, seg_end in schedule.open_segments(day):
            position = max(start, seg_start)
            if position >= seg_end:
                continue
            available = (seg_end - position).seconds
            if remaining > available:
                remaining -= available
                continue
            candidate = position + Duration(remaining)
            return schedule._jump_out_of_closed(candidate)
        day += timedelta(days=1)
