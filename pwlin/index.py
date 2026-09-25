"""Per-channel segment index with strictly local rebuilds.

Each channel keeps a sorted breakpoint list plus one :class:`Segment` per
adjacent pair, caching the value bound of that segment for fast inverse
pruning.  Inserting or deleting a breakpoint only touches the segments
adjacent to it -- the rest of the index is left untouched.  The
``rebuilt_segments`` counter is telemetry proving the locality.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass, field
from typing import Callable

from .model import Measurement


@dataclass(frozen=True)
class Segment:
    """Cached bounds of one piecewise-linear segment of one channel."""

    x0: float
    x1: float
    bound_lo: float  # min of the lower envelope over the segment
    bound_hi: float  # max of the upper envelope over the segment


@dataclass
class ChannelIndex:
    xs: list = field(default_factory=list)      # sorted breakpoints
    segs: list = field(default_factory=list)    # len == len(xs) - 1


class SegmentIndex:
    def __init__(self) -> None:
        self._channels: dict = {}
        self.rebuilt_segments = 0  # telemetry: segments (re)built so far

    def clone(self) -> "SegmentIndex":
        other = SegmentIndex()
        other._channels = {
            name: ChannelIndex(list(ci.xs), list(ci.segs))
            for name, ci in self._channels.items()
        }
        other.rebuilt_segments = self.rebuilt_segments
        return other

    def channel(self, name: str) -> ChannelIndex:
        return self._channels.get(name, ChannelIndex())

    def channel_names(self) -> tuple:
        return tuple(sorted(self._channels))

    @staticmethod
    def _seg(channel: str, x0: float, x1: float,
             get_meas: Callable[[float, str], Measurement]) -> Segment:
        m0 = get_meas(x0, channel)
        m1 = get_meas(x1, channel)
        return Segment(x0, x1, min(m0.lo, m1.lo), max(m0.hi, m1.hi))

    def insert(self, channel: str, x: float,
               get_meas: Callable[[float, str], Measurement]) -> None:
        """Insert breakpoint ``x`` (or refresh it if the measurement changed).

        Only the one spanning segment is replaced by two new ones; when the
        breakpoint already exists only its two adjacent segments are rebuilt.
        """
        ci = self._channels.setdefault(channel, ChannelIndex())
        xs, segs = ci.xs, ci.segs
        i = bisect.bisect_left(xs, x)
        if i < len(xs) and xs[i] == x:
            for j in (i - 1, i):
                if 0 <= j < len(segs):
                    segs[j] = self._seg(channel, xs[j], xs[j + 1], get_meas)
                    self.rebuilt_segments += 1
            return
        if 0 < i < len(xs):
            del segs[i - 1]  # the single spanning segment
            self.rebuilt_segments += 1
        xs.insert(i, x)
        if i > 0:
            segs.insert(i - 1, self._seg(channel, xs[i - 1], x, get_meas))
            self.rebuilt_segments += 1
        if i < len(xs) - 1:
            segs.insert(i, self._seg(channel, x, xs[i + 1], get_meas))
            self.rebuilt_segments += 1

    def delete(self, channel: str, x: float,
               get_meas: Callable[[float, str], Measurement]) -> None:
        """Remove breakpoint ``x``; only its two adjacent segments are merged."""
        ci = self._channels[channel]
        xs, segs = ci.xs, ci.segs
        i = bisect.bisect_left(xs, x)
        if not (i < len(xs) and xs[i] == x):
            raise KeyError(f"no breakpoint at x={x!r} for channel {channel!r}")
        if 0 < i < len(xs) - 1:
            del segs[i - 1:i + 1]
            self.rebuilt_segments += 2
            del xs[i]
            segs.insert(i - 1, self._seg(channel, xs[i - 1], xs[i], get_meas))
            self.rebuilt_segments += 1
        else:
            if segs:
                if i == 0:
                    del segs[0]
                else:
                    del segs[-1]
                self.rebuilt_segments += 1
            del xs[i]
