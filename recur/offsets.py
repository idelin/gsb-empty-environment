"""Synthetic UTC-offset transition tables and wall<->instant conversion.

A table is a *fixture*: an explicit, ordered list of
``(instant, utc_offset_us, abbr)`` transitions supplied by the caller rather
than discovered from the host zone database.  Each row names the offset that
is in force starting at that instant, until the next transition.

The interval of local (wall) readings produced by an offset segment is
computed directly, which makes gaps ("spring forward") and folds ("fall
back") explicit and testable with stable integer arithmetic.
"""

from __future__ import annotations

import bisect
import datetime as _dt
import hashlib
import json
from dataclasses import dataclass

from .time_model import (
    Instant,
    WallTime,
    _EPOCH_DATE,
    _USEC_PER_DAY,
)

DAY_US = _USEC_PER_DAY


class Gap:
    """What to do when a wall reading falls in a skipped (non-existent) time."""

    RAISE = "raise"          # the wall time does not exist on this time line
    FORWARD = "forward"      # move to the first instant after the jump
    BACKWARD = "backward"    # move to the last instant before the jump


class Ambiguous:
    """What to do when a wall reading occurs twice during a fold."""

    RAISE = "raise"             # require explicit disambiguation
    EARLIEST = "earliest"       # first occurrence (pre-transition offset)
    LATEST = "latest"           # second occurrence (post-transition offset)
    BOTH = "both"               # yield both instants


class AmbiguousWallTime(ValueError):
    """Raised for ambiguous wall readings resolved with ``RAISE``."""


class NonexistentWallTime(ValueError):
    """Raised for gap wall readings resolved with ``RAISE``."""


@dataclass(frozen=True)
class Transition:
    at: Instant                 # instant at which the new offset takes effect
    utc_offset_us: int          # offset in force from ``at`` onward
    abbr: str = ""

    def canonical(self) -> list:
        return [self.at.epoch_us, self.utc_offset_us, self.abbr]


@dataclass(frozen=True)
class Resolved:
    """Result of resolving a wall reading.

    ``kind`` is ``"unique"``, ``"gap"`` or ``"ambiguous"``.  Gap policies
    FORWARD/BACKWARD fill ``instant`` and ``adjusted_to``; ``Ambiguous.BOTH``
    fills ``instants`` instead of ``instant``.
    """

    kind: str
    offset_us: int
    instant: Instant | None = None
    instants: tuple[Instant, ...] = ()
    adjusted_to: WallTime | None = None


class OffsetTable:
    """A piecewise-constant UTC-offset function defined by explicit rows."""

    def __init__(self, transitions: list[Transition], name: str = "synthetic"):
        if not transitions:
            raise ValueError("an offset table needs at least one transition")
        rows = sorted(transitions, key=lambda t: t.at.epoch_us)
        for prev, cur in zip(rows, rows[1:]):
            if prev.at.epoch_us == cur.at.epoch_us:
                raise ValueError("transitions must have distinct instants")
        self.name = name
        self._at = [r.at.epoch_us for r in rows]
        self._off = [r.utc_offset_us for r in rows]
        self._rows = rows
        # Local interval [early, late) of readings mapped by each segment.
        self._early = [a + o for a, o in zip(self._at, self._off)]
        # Each segment maps wall readings up to (but excluding) the next
        # transition instant, read under that *transition* instant's segment
        # offset -- hence ``late`` uses the current segment's own offset.
        self._late = [
            (rows[i + 1].at.epoch_us + self._off[i]) for i in range(len(rows) - 1)
        ] + [self._early[-1] + 400 * 365 * DAY_US]
        payload = json.dumps(
            [r.canonical() for r in rows], separators=(",", ":"), sort_keys=True
        )
        digest = hashlib.sha1(payload.encode("ascii")).hexdigest()[:12]
        self.version = f"{name}:{digest}"

    # -- queries -----------------------------------------------------------

    def offset_at(self, instant: Instant) -> int:
        idx = bisect.bisect_right(self._at, instant.epoch_us) - 1
        return self._off[idx]

    def wall_at(self, instant: Instant) -> tuple[WallTime, int]:
        """Return the wall reading and active offset at an instant."""
        idx = bisect.bisect_right(self._at, instant.epoch_us) - 1
        offset = self._off[idx]
        local_epoch = instant.epoch_us + offset
        days, tod = divmod(local_epoch, DAY_US)  # floor division handles negatives
        date = _EPOCH_DATE + _dt.timedelta(days=days)
        return WallTime(date.year, date.month, date.day, tod), offset

    def resolve(
        self,
        wall: WallTime,
        *,
        gap: str = Gap.FORWARD,
        ambiguous: str = Ambiguous.EARLIEST,
    ) -> Resolved:
        """Map a wall reading to instant(s) under explicit policies.

        Every offset segment whose local interval ``[early, late)`` contains
        the reading is a candidate.  Zero candidates means a gap, one means a
        unique reading and two means a fold (the reading happens twice).
        """
        x = wall.epoch_us
        j = bisect.bisect_right(self._early, x) - 1
        candidates = [i for i in (j - 1, j) if i >= 0 and self._early[i] <= x < self._late[i]]

        if len(candidates) == 1:
            i = candidates[0]
            return Resolved("unique", self._off[i], Instant(x - self._off[i]))
        if len(candidates) == 2:
            i0, i1 = candidates
            pair = tuple(
                sorted(
                    (Instant(x - self._off[i0]), Instant(x - self._off[i1])),
                    key=lambda v: v.epoch_us,
                )
            )
            return self._ambiguous(x, pair, ambiguous)

        # No segment maps this reading: it lies in a gap between segments.
        if j < 0:
            off = self._off[0]
            return Resolved("unique", off, Instant(x - off))
        return self._gap(j, gap)

    def _ambiguous(self, x, pair, policy):
        if policy == Ambiguous.RAISE:
            raise AmbiguousWallTime("wall reading is ambiguous")
        if policy == Ambiguous.BOTH:
            return Resolved("ambiguous", x - pair[0].epoch_us, None, pair)
        if policy == Ambiguous.EARLIEST:
            chosen = pair[0]
        elif policy == Ambiguous.LATEST:
            chosen = pair[1]
        else:
            raise ValueError(f"unknown ambiguous policy: {policy!r}")
        return Resolved("ambiguous", x - chosen.epoch_us, chosen)

    def _gap(self, i, policy):
        if policy == Gap.RAISE:
            raise NonexistentWallTime("wall reading does not exist (skipped)")
        if policy == Gap.FORWARD:
            edge = self._at[i + 1]
        elif policy == Gap.BACKWARD:
            edge = self._at[i + 1] - 1
        else:
            raise ValueError(f"unknown gap policy: {policy!r}")
        inst = Instant(edge)
        adjusted, off = self.wall_at(inst)
        return Resolved("gap", off, inst, (), adjusted)
