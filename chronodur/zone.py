"""Synthetic offset-transition tables and exact wall/instant conversion.

Three distinct time concepts are modelled as three distinct types:

- ``Instant``  -- an absolute point in time (exact Fraction seconds since
  the Unix epoch), independent of any zone.
- ``WallTime`` -- a local wall-clock reading in a given zone; it is *not*
  an absolute time and may be invalid (gap) or ambiguous (fold).
- ``Duration`` -- an exact length of time (see model.py); subtracting two
  Instants yields one.
"""
from __future__ import annotations

import datetime as dt
from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction

from .errors import WallTimeError
from .model import Duration, floor_div

EPOCH = dt.datetime(1970, 1, 1)


@dataclass(frozen=True)
class Transition:
    """At ``utc_seconds`` the zone switches to ``offset_seconds`` (UTC+offset)."""

    utc_seconds: int
    offset_seconds: int
    abbrev: str = ""


class SyntheticZone:
    """A timezone built from an explicit, synthetic transition table."""

    def __init__(self, name, transitions, base_offset_seconds=0):
        self.name = name
        self.base_offset_seconds = base_offset_seconds
        self.transitions = tuple(sorted(transitions, key=lambda t: t.utc_seconds))
        self._times = [t.utc_seconds for t in self.transitions]

    def offset_at(self, instant_seconds: Fraction) -> int:
        idx = bisect_right(self._times, instant_seconds)
        if idx == 0:
            return self.base_offset_seconds
        return self.transitions[idx - 1].offset_seconds

    def __repr__(self):
        return f"SyntheticZone({self.name!r})"


@dataclass(frozen=True)
class Instant:
    """An absolute point in time: exact seconds since 1970-01-01T00:00:00Z."""

    seconds: Fraction

    def to_wall(self, zone: SyntheticZone) -> "WallTime":
        return instant_to_wall(self, zone)

    def __sub__(self, other):
        if isinstance(other, Instant):
            return Duration(fixed=self.seconds - other.seconds)
        return NotImplemented


@dataclass(frozen=True)
class WallTime:
    """A local wall-clock reading in a zone (not an absolute time)."""

    dt: dt.datetime
    zone: SyntheticZone
    sub: Fraction = Fraction(0)  # exact sub-second part in [0, 1)

    def to_instant(self) -> Instant:
        return wall_to_instant(self)


def wall_to_instant(wall: WallTime) -> Instant:
    """Resolve a wall time to an Instant.

    Raises WallTimeError if the wall time falls in an offset gap. In a
    fold (ambiguous wall time) the earlier instant is chosen.
    """
    delta = wall.dt - EPOCH
    local = Fraction(delta.days * 86400 + delta.seconds) + wall.sub
    zone = wall.zone
    offsets = {zone.base_offset_seconds}
    offsets.update(t.offset_seconds for t in zone.transitions)
    candidates = []
    for off in offsets:
        t = local - off
        if zone.offset_at(t) == off:
            candidates.append(t)
    if not candidates:
        raise WallTimeError(
            f"{wall.dt} does not exist in zone {zone.name!r} (offset gap)")
    return Instant(min(candidates))


def instant_to_wall(instant: Instant, zone: SyntheticZone) -> WallTime:
    off = zone.offset_at(instant.seconds)
    local = instant.seconds + off
    days = floor_div(local, 86400)
    rem = local - days * 86400
    secs = floor_div(rem, 1)
    sub = rem - secs
    return WallTime(EPOCH + dt.timedelta(days=days, seconds=secs), zone, sub)
