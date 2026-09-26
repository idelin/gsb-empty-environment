"""Precise time primitives.

Three distinct concepts are modeled explicitly:

- ``WallClock``: a naive local date-time ("what the wall clock shows"),
  with no offset attached. Recurrence rules are defined in wall time.
- ``Instant``: an absolute point on the UTC timeline, stored as integer
  seconds since the Unix epoch. Obtained from a ``WallClock`` only by
  applying an ``OffsetTable``.
- ``Duration``: a signed length of time in exact seconds. Adding a
  ``Duration`` to an ``Instant`` is always safe; it is *not* how monthly
  rules advance (they advance in calendar months of wall time).

All representations are exact integer-second precision; sub-second
values are rejected so that equality, ordering and dedup are total.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone

_NAIVE_EPOCH = datetime(1970, 1, 1)

SECONDS_PER_DAY = 86400


@dataclass(frozen=True, order=True)
class Duration:
    """A signed length of time in exact seconds."""

    seconds: int

    def __post_init__(self) -> None:
        if not isinstance(self.seconds, int):
            raise TypeError("Duration must be an integer number of seconds")

    @classmethod
    def days(cls, n: int) -> "Duration":
        return cls(n * SECONDS_PER_DAY)

    @classmethod
    def hours(cls, n: int) -> "Duration":
        return cls(n * 3600)

    def __neg__(self) -> "Duration":
        return Duration(-self.seconds)


@dataclass(frozen=True, order=True)
class WallClock:
    """A naive local date-time. Never interpreted as UTC by this library."""

    dt: datetime

    def __post_init__(self) -> None:
        if self.dt.tzinfo is not None:
            raise ValueError("WallClock must be timezone-naive")
        if self.dt.microsecond:
            raise ValueError("WallClock requires whole-second precision")

    @property
    def seconds(self) -> int:
        """Wall time encoded as naive epoch seconds (NOT a UTC instant)."""
        delta = self.dt - _NAIVE_EPOCH
        return delta.days * SECONDS_PER_DAY + delta.seconds

    @classmethod
    def from_seconds(cls, seconds: int) -> "WallClock":
        return cls(_NAIVE_EPOCH + timedelta(seconds=seconds))

    def __str__(self) -> str:  # pragma: no cover - display helper
        return self.dt.isoformat()


@dataclass(frozen=True, order=True)
class Instant:
    """An absolute point in time: integer seconds since the Unix epoch (UTC)."""

    epoch_seconds: int

    def __post_init__(self) -> None:
        if not isinstance(self.epoch_seconds, int):
            raise TypeError("Instant must be an integer number of epoch seconds")

    @classmethod
    def from_utc(cls, dt: datetime) -> "Instant":
        if dt.tzinfo is None:
            raise ValueError("Instant.from_utc requires an aware datetime")
        return cls(int(dt.timestamp()))

    def to_utc(self) -> datetime:
        return datetime.fromtimestamp(self.epoch_seconds, tz=timezone.utc)

    def plus(self, duration: Duration) -> "Instant":
        return Instant(self.epoch_seconds + duration.seconds)

    def minus(self, other: "Instant") -> Duration:
        return Duration(self.epoch_seconds - other.epoch_seconds)

    def __str__(self) -> str:  # pragma: no cover - display helper
        return self.to_utc().isoformat()
