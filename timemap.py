"""Piecewise-affine mapping between output time and source time.

The mapping is described by control points ``(out_t, src_t)`` with strictly
increasing output times.  Each segment between two consecutive control points
is affine, and its slope determines the segment kind:

* slope > 0  -> FORWARD  (normal or time-stretched playback)
* slope < 0  -> REVERSE  (backward playback)
* slope == 0 -> FREEZE   (the same source position is repeated)

Because the map is a function of *output* time, repeated source regions
(segment repeats), reversed regions and frozen regions all keep their source
identity, and compound edits can never collapse into a single speed factor.
"""
from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from enum import Enum
from typing import Sequence, Tuple


class Kind(Enum):
    FORWARD = "forward"
    REVERSE = "reverse"
    FREEZE = "freeze"


@dataclass(frozen=True)
class Segment:
    """One affine piece of the time map (output times are half-open)."""

    out_start: float
    out_end: float
    src_start: float
    src_end: float

    @property
    def slope(self) -> float:
        return (self.src_end - self.src_start) / (self.out_end - self.out_start)

    @property
    def kind(self) -> Kind:
        s = self.slope
        if s > 0.0:
            return Kind.FORWARD
        if s < 0.0:
            return Kind.REVERSE
        return Kind.FREEZE

    def source_at(self, out_t: float) -> float:
        return self.src_start + (out_t - self.out_start) * self.slope


class PiecewiseTimeMap:
    """Immutable piecewise-affine map from output time to source position."""

    def __init__(self, control_points: Sequence[Tuple[float, float]]):
        pts = [(float(o), float(s)) for o, s in control_points]
        if len(pts) < 2:
            raise ValueError("need at least two control points")
        for (o0, _), (o1, _) in zip(pts, pts[1:]):
            if not o1 > o0:
                raise ValueError("output times must be strictly increasing")
        self._points: Tuple[Tuple[float, float], ...] = tuple(pts)
        self._out_times: Tuple[float, ...] = tuple(o for o, _ in pts)
        self._segments: Tuple[Segment, ...] = tuple(
            Segment(pts[i][0], pts[i + 1][0], pts[i][1], pts[i + 1][1])
            for i in range(len(pts) - 1)
        )

    @property
    def control_points(self) -> Tuple[Tuple[float, float], ...]:
        return self._points

    @property
    def segments(self) -> Tuple[Segment, ...]:
        return self._segments

    @property
    def out_start(self) -> float:
        return self._out_times[0]

    @property
    def out_end(self) -> float:
        return self._out_times[-1]

    def segment_index(self, out_t: float) -> int:
        """Index of the segment owning ``out_t`` (half-open intervals).

        A boundary output time belongs to the *following* segment, so a
        turning point is emitted exactly once and never replayed twice.
        """
        i = bisect_right(self._out_times, out_t) - 1
        return min(max(i, 0), len(self._segments) - 1)

    def source_at(self, out_t: float) -> float:
        return self._segments[self.segment_index(out_t)].source_at(out_t)

    def kind_at(self, out_t: float) -> Kind:
        return self._segments[self.segment_index(out_t)].kind
