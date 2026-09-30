"""Synthetic offset conversion tables.

A free-running clock whose rate is re-estimated over time is described by
piecewise-affine segments.  Offsets are *synthesized* so the mapping stays
continuous across segment boundaries.  Everything is exact Fraction math.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction

from .rounding import RoundingPolicy, round_fraction


@dataclass(frozen=True)
class Segment:
    start: Fraction     # first source tick covered by this segment
    scale: Fraction     # seconds per tick inside the segment
    offset: Fraction    # exact time (seconds) at `start`

    def __post_init__(self):
        for f in ("start", "scale", "offset"):
            object.__setattr__(self, f, Fraction(getattr(self, f)))
        if self.scale <= 0:
            raise ValueError("segment scale must be positive")

    def end_time(self, next_start) -> Fraction:
        return self.offset + (Fraction(next_start) - self.start) * self.scale


class SyntheticOffsetTable:
    def __init__(self, segments):
        segs = list(segments)
        if not segs:
            raise ValueError("table needs at least one segment")
        for prev, nxt in zip(segs, segs[1:]):
            if nxt.start <= prev.start:
                raise ValueError("segment starts must be strictly increasing")
        self._segments = tuple(segs)
        self._starts = [s.start for s in segs]

    @classmethod
    def synthesize(cls, start_tick, start_seconds, pieces) -> "SyntheticOffsetTable":
        """Build a continuous table from (num_ticks, seconds_per_tick) pieces."""
        segments = []
        tick = Fraction(start_tick)
        t = Fraction(start_seconds)
        for num_ticks, scale in pieces:
            num_ticks, scale = Fraction(num_ticks), Fraction(scale)
            segments.append(Segment(tick, scale, t))
            t += num_ticks * scale
            tick += num_ticks
        return cls(segments)

    @property
    def segments(self) -> tuple:
        return self._segments

    def segment_for(self, tick) -> Segment:
        i = bisect_right(self._starts, Fraction(tick)) - 1
        if i < 0:
            raise ValueError(f"tick {tick} before table start {self._starts[0]}")
        return self._segments[i]

    def _segment_for_time(self, seconds: Fraction) -> Segment:
        for prev, nxt in zip(self._segments, self._segments[1:]):
            if seconds < prev.end_time(nxt.start):
                return prev
        return self._segments[-1]

    def to_seconds(self, tick) -> Fraction:
        seg = self.segment_for(tick)
        return seg.offset + (Fraction(tick) - seg.start) * seg.scale

    def to_ticks(self, seconds, policy=RoundingPolicy.HALF_EVEN) -> int:
        seconds = Fraction(seconds)
        seg = self._segment_for_time(seconds)
        exact = seg.start + (seconds - seg.offset) / seg.scale
        return round_fraction(exact, policy)

    def ticks_covering(self, s_lo, s_hi) -> range:
        """All integer ticks whose exact time falls in [s_lo, s_hi]."""
        first = self.to_ticks(s_lo, RoundingPolicy.CEILING)
        last = self.to_ticks(s_hi, RoundingPolicy.FLOOR)
        return range(first, last + 1)
