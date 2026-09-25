"""Exact time primitives: Duration, Instant, WallTime, and a synthetic offset table.

Three distinct concepts are modelled as three distinct types:

- ``Duration``: a length of time (exact whole seconds), no position on any axis.
- ``Instant``:  an absolute point in time (seconds since the Unix epoch, UTC).
- ``WallTime``: a local wall-clock reading with no offset attached.

``OffsetTable`` is a synthetic UTC-offset transition table that stands in for a
real time-zone database.  Conversions across an offset transition always
reflect the *real* elapsed time.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone

_EPOCH_AWARE = datetime(1970, 1, 1, tzinfo=timezone.utc)
_EPOCH_NAIVE = datetime(1970, 1, 1)


def _whole_seconds(delta: timedelta) -> int:
    if delta.microseconds:
        raise ValueError("only whole-second precision is supported")
    return delta.days * 86400 + delta.seconds


@dataclass(frozen=True, order=True)
class Duration:
    """An exact length of time in whole seconds."""

    seconds: int = 0

    def __post_init__(self) -> None:
        if isinstance(self.seconds, bool) or not isinstance(self.seconds, int):
            raise TypeError("Duration must be built from an int number of seconds")

    @classmethod
    def of(cls, *, hours: int = 0, minutes: int = 0, seconds: int = 0) -> "Duration":
        return cls(hours * 3600 + minutes * 60 + seconds)

    def __add__(self, other: object) -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.seconds + other.seconds)

    def __sub__(self, other: object) -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.seconds - other.seconds)

    def __neg__(self) -> "Duration":
        return Duration(-self.seconds)

    def __str__(self) -> str:
        sign = "-" if self.seconds < 0 else ""
        hours, rem = divmod(abs(self.seconds), 3600)
        minutes, secs = divmod(rem, 60)
        return f"{sign}{hours}h{minutes:02d}m{secs:02d}s"


@dataclass(frozen=True, order=True)
class Instant:
    """An absolute point in time: whole seconds since the Unix epoch (UTC)."""

    epoch_seconds: int

    def __post_init__(self) -> None:
        if isinstance(self.epoch_seconds, bool) or not isinstance(self.epoch_seconds, int):
            raise TypeError("Instant must be built from an int number of epoch seconds")

    @classmethod
    def from_utc(cls, dt: datetime) -> "Instant":
        if dt.tzinfo is None:
            raise ValueError("an aware (tzinfo-bearing) datetime is required")
        return cls(_whole_seconds(dt.astimezone(timezone.utc) - _EPOCH_AWARE))

    def to_utc(self) -> datetime:
        return _EPOCH_AWARE + timedelta(seconds=self.epoch_seconds)

    def __add__(self, other: object) -> "Instant":
        if not isinstance(other, Duration):
            return NotImplemented
        return Instant(self.epoch_seconds + other.seconds)

    def __sub__(self, other: object) -> object:
        if isinstance(other, Duration):
            return Instant(self.epoch_seconds - other.seconds)
        if isinstance(other, Instant):
            return Duration(self.epoch_seconds - other.epoch_seconds)
        return NotImplemented

    def __str__(self) -> str:
        return self.to_utc().strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass(frozen=True, order=True)
class WallTime:
    """A local wall-clock reading (naive datetime, no offset attached)."""

    naive: datetime

    def __post_init__(self) -> None:
        if self.naive.tzinfo is not None:
            raise ValueError("WallTime must be naive (no tzinfo)")
        if self.naive.microsecond:
            raise ValueError("only whole-second precision is supported")

    @classmethod
    def of(cls, year: int, month: int, day: int,
           hour: int = 0, minute: int = 0, second: int = 0) -> "WallTime":
        return cls(datetime(year, month, day, hour, minute, second))

    def __str__(self) -> str:
        return self.naive.strftime("%Y-%m-%d %H:%M:%S") + " (wall)"


@dataclass(frozen=True)
class OffsetTransition:
    """At instant ``at`` (UTC) the UTC offset becomes ``offset_after``."""

    at: Instant
    offset_after: Duration


class OffsetTable:
    """Synthetic UTC-offset transition table.

    ``to_instant`` resolves wall readings deterministically:

    - normal reading: the unique matching instant;
    - repeated reading (offset falls back): the earliest matching instant;
    - skipped reading (offset springs forward): the first instant whose wall
      reading is at/after the requested one, i.e. the transition instant.
    """

    def __init__(self, base_offset: Duration,
                 transitions: tuple[OffsetTransition, ...] = ()) -> None:
        self._base = base_offset
        self._transitions = tuple(sorted(transitions, key=lambda t: t.at))

    def offset_at(self, instant: Instant) -> Duration:
        offset = self._base
        for transition in self._transitions:
            if transition.at <= instant:
                offset = transition.offset_after
            else:
                break
        return offset

    def to_wall(self, instant: Instant) -> WallTime:
        shifted = instant.to_utc() + timedelta(seconds=self.offset_at(instant).seconds)
        return WallTime(shifted.replace(tzinfo=None))

    def to_instant(self, wall: WallTime) -> Instant:
        target = wall.naive
        wall_epoch = _whole_seconds(target - _EPOCH_NAIVE)
        offsets = {self._base} | {t.offset_after for t in self._transitions}
        candidates = []
        for offset in offsets:
            candidate = Instant(wall_epoch - offset.seconds)
            if self.to_wall(candidate).naive == target:
                candidates.append(candidate)
        if candidates:
            return min(candidates)
        # Skipped wall reading (spring forward): snap to the transition instant.
        for transition in self._transitions:
            offset_before = self.offset_at(transition.at - Duration(1))
            before = WallTime(
                (transition.at.to_utc() + timedelta(seconds=offset_before.seconds))
                .replace(tzinfo=None))
            after = self.to_wall(transition.at)
            if before.naive <= target < after.naive:
                return transition.at
        raise ValueError(f"wall time {wall} is not representable in this offset table")
