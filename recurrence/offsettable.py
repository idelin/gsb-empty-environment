"""Synthetic offset transition table (a stand-in for a tz database).

The table is a versioned list of transitions. Each transition changes the
UTC offset at a given instant. Converting a :class:`WallClock` to an
:class:`Instant` can hit two anomalies, each handled by an explicit,
caller-chosen policy:

- gap (wall time skipped, e.g. spring forward): ``GapPolicy.SHIFT_FORWARD``
  shifts the wall time forward by the gap length and resolves that, or
  ``GapPolicy.REJECT`` raises :class:`NonexistentWallTime`.
- fold (wall time repeated, e.g. fall back): ``FoldPolicy.EARLIER`` picks
  the earlier of the two matching instants, ``FoldPolicy.LATER`` the later.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .timeprims import Instant, WallClock


class GapPolicy(Enum):
    SHIFT_FORWARD = "shift_forward"
    REJECT = "reject"


class FoldPolicy(Enum):
    EARLIER = "earlier"
    LATER = "later"


class NonexistentWallTime(ValueError):
    pass


@dataclass(frozen=True)
class Transition:
    utc_epoch: int       # instant at which the offset changes
    offset_before: int   # seconds east of UTC, valid before the instant
    offset_after: int    # seconds east of UTC, valid from the instant on


class OffsetTable:
    def __init__(self, version: str, base_offset: int, transitions=()):
        self.version = version
        self.base_offset = base_offset
        self.transitions = tuple(sorted(transitions, key=lambda t: t.utc_epoch))
        for prev, nxt in zip(self.transitions, self.transitions[1:]):
            if prev.utc_epoch >= nxt.utc_epoch:
                raise ValueError("transitions must have distinct instants")
            if prev.offset_after != nxt.offset_before:
                raise ValueError("transition offsets must chain")

    def offset_at(self, instant: Instant) -> int:
        t = instant.epoch_seconds
        offset = self.base_offset
        for tr in self.transitions:
            if t >= tr.utc_epoch:
                offset = tr.offset_after
            else:
                break
        return offset

    def to_wall(self, instant: Instant) -> WallClock:
        return WallClock.from_seconds(instant.epoch_seconds + self.offset_at(instant))

    def to_instant(
        self,
        wall: WallClock,
        gap_policy: GapPolicy = GapPolicy.SHIFT_FORWARD,
        fold_policy: FoldPolicy = FoldPolicy.EARLIER,
    ) -> Instant:
        w = wall.seconds
        offsets = {self.base_offset}
        for tr in self.transitions:
            offsets.add(tr.offset_before)
            offsets.add(tr.offset_after)
        valid = sorted({w - o for o in offsets if self.offset_at(Instant(w - o)) == o})
        if len(valid) == 1:
            return Instant(valid[0])
        if len(valid) >= 2:
            # fold: the wall time exists at two (or more) instants
            return Instant(valid[0] if fold_policy is FoldPolicy.EARLIER else valid[-1])
        # gap: locate the transition whose skipped range contains w
        for tr in self.transitions:
            gap_len = tr.offset_after - tr.offset_before
            if gap_len <= 0:
                continue
            gap_start_wall = tr.utc_epoch + tr.offset_before
            if gap_start_wall <= w < gap_start_wall + gap_len:
                if gap_policy is GapPolicy.REJECT:
                    raise NonexistentWallTime(
                        f"wall time {wall} does not exist (gap of {gap_len}s)"
                    )
                # shift forward by the gap length, then resolve normally
                return Instant(w + gap_len - tr.offset_after)
        raise NonexistentWallTime(f"wall time {wall} does not exist")  # pragma: no cover
