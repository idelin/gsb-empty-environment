"""Piecewise gain curve with linear crossfades at segment boundaries.

The curve is a pure function of the sample index: ``gain_at(n)`` does not
depend on any processing chunk boundaries, which is what makes applying a
fixed plan in arbitrary chunks bit-identical to applying it in one block.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass
from typing import List, Optional, Sequence, Set, Tuple


@dataclass
class CurveSegment:
    start: int
    end: int
    gain: float


class GainCurve:
    """Constant gain per segment, linearly crossfaded around boundaries.

    A crossfade of ``fade_len`` samples is centered on each interior
    boundary and clamped so it never extends past the adjacent segments
    (this keeps very short segments well-defined). ``fade_len == 0`` means
    a hard gain step at the boundary.
    """

    def __init__(self, segments: Sequence[CurveSegment], fade_len: int = 0):
        if not segments:
            raise ValueError("a gain curve needs at least one segment")
        self.segments: List[CurveSegment] = list(segments)
        self.fade_len = max(0, int(fade_len))
        self._starts = [s.start for s in self.segments]

    def _fade_region(self, boundary: int) -> Optional[Tuple[int, int]]:
        """Sample range [lo, hi) of the crossfade at ``boundary``.

        ``boundary`` is the index of the segment that *starts* at the
        boundary position (1 .. len(segments)-1).
        """
        k = boundary
        if k <= 0 or k >= len(self.segments) or self.fade_len == 0:
            return None
        b = self.segments[k].start
        half = self.fade_len // 2
        lo = max(b - half, self.segments[k - 1].start)
        hi = min(b - half + self.fade_len, self.segments[k].end)
        if hi <= lo:
            return None
        return lo, hi

    def _locate(self, n: int) -> int:
        i = bisect.bisect_right(self._starts, n) - 1
        if i < 0:
            i = 0
        elif i >= len(self.segments):
            i = len(self.segments) - 1
        return i

    def gain_at(self, n: int) -> float:
        """Gain applied to sample ``n``; deterministic in ``n`` only."""
        i = self._locate(n)
        # Boundary to the left of n (between segment i-1 and i) takes
        # precedence if both fades cover n (possible for tiny segments).
        for k in (i, i + 1):
            region = self._fade_region(k)
            if region is None:
                continue
            lo, hi = region
            if lo <= n < hi:
                t = (n - lo) / (hi - lo)
                g0 = self.segments[k - 1].gain
                g1 = self.segments[k].gain
                return g0 + (g1 - g0) * t
        return self.segments[i].gain

    def influencing_segments(self, n: int) -> Set[int]:
        """Indices of segments whose gains affect sample ``n``."""
        i = self._locate(n)
        idxs = {i}
        for k in (i, i + 1):
            region = self._fade_region(k)
            if region is not None and region[0] <= n < region[1]:
                idxs.add(k - 1)
                idxs.add(k)
        return idxs
