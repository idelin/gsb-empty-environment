"""Synthetic offset conversion tables between clock timelines.

A table is a piecewise-affine, exactly represented mapping
``dst = src * rate + offset`` with rational coefficients.  Tables are
versioned: clock calibration produces a new table version while old
versions stay available for historical cursors.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .exact import TimePoint, WallClockTime, as_fraction
from .intervals import Interval

__all__ = ["AffineSegment", "OffsetTable", "Clock"]


@dataclass(frozen=True)
class AffineSegment:
    """One affine piece; ``start=None`` (minus infinity) for the first only."""

    start: Fraction | None
    rate: Fraction
    offset: Fraction

    def __post_init__(self):
        if self.start is not None:
            object.__setattr__(self, "start", as_fraction(self.start))
        object.__setattr__(self, "rate", as_fraction(self.rate))
        object.__setattr__(self, "offset", as_fraction(self.offset))
        if self.rate <= 0:
            raise ValueError("segment rate must be positive")

    def forward(self, t: Fraction) -> Fraction:
        return t * self.rate + self.offset

    def backward(self, u: Fraction) -> Fraction:
        return (u - self.offset) / self.rate


class OffsetTable:
    """Piecewise-affine exact conversion ``src_clock -> dst_clock``."""

    def __init__(self, src_clock: str, dst_clock: str, segments, version=0):
        segs = sorted(segments, key=lambda s: (s.start is not None, s.start))
        if not segs:
            raise ValueError("a table needs at least one segment")
        for seg in segs[1:]:
            if seg.start is None:
                raise ValueError("only the first segment may start at -infinity")
        for prev, cur in zip(segs, segs[1:]):
            if prev.start == cur.start:
                raise ValueError("duplicate segment start")
            if prev.forward(cur.start) > cur.forward(cur.start):
                raise ValueError("table must be non-decreasing (no backward time jumps)")
        self.src_clock = src_clock
        self.dst_clock = dst_clock
        self.segments = tuple(segs)
        self.version = version

    def _segment_at(self, t: Fraction | None) -> AffineSegment:
        if t is None:
            return self.segments[0]
        chosen = self.segments[0]
        for seg in self.segments:
            if seg.start is None or seg.start <= t:
                chosen = seg
            else:
                break
        return chosen

    def forward(self, t: Fraction) -> Fraction:
        return self._segment_at(t).forward(t)

    def backward(self, u: Fraction) -> Fraction:
        """Inverse map; dst holes left by offset jumps clamp to the jump point."""
        segs = self.segments
        idx = 0
        for i, seg in enumerate(segs):
            dst_start = None if seg.start is None else seg.forward(seg.start)
            if dst_start is None or dst_start <= u:
                idx = i
            else:
                break
        seg = segs[idx]
        t = seg.backward(u)
        lo = seg.start
        hi = segs[idx + 1].start if idx + 1 < len(segs) else None
        if lo is not None and t < lo:
            t = lo
        if hi is not None and t > hi:
            t = hi
        return t

    def forward_interval(self, interval: Interval) -> list[Interval]:
        """Map an interval piece by piece.

        Discontinuity holes introduced by offset jumps are excluded from the
        result, never bridged: no source time maps into them.
        """
        if interval.is_empty():
            return []
        points = [interval.start]
        for seg in self.segments[1:]:
            bp = seg.start
            if interval.start is not None and bp <= interval.start:
                continue
            if interval.end is not None and (
                    bp > interval.end or (bp == interval.end and not interval.closed_end)):
                continue
            points.append(bp)
        if interval.end is not None and points[-1] != interval.end:
            points.append(interval.end)
        pieces = []
        for i in range(len(points) - 1):
            a, b = points[i], points[i + 1]
            seg = self._segment_at(a)
            pieces.append(Interval(
                None if a is None else seg.forward(a),
                None if b is None else seg.forward(b),
                interval.closed_start if i == 0 else True,
                False,
            ))
        a = points[-1]
        seg = self._segment_at(a)
        pieces.append(Interval(
            None if a is None else seg.forward(a),
            None if interval.end is None else seg.forward(interval.end),
            interval.closed_start if len(points) == 1 else True,
            interval.closed_end,
        ))
        return [p for p in pieces if not p.is_empty()]

    def compose(self, other: "OffsetTable") -> "OffsetTable":
        """Compose ``self`` (A->B) with ``other`` (B->C) into a direct A->C table."""
        if self.dst_clock != other.src_clock:
            raise ValueError(
                f"cannot compose {self.dst_clock!r} with {other.src_clock!r}")
        bps = sorted(
            {s.start for s in self.segments[1:]}
            | {self.backward(s.start) for s in other.segments[1:]}
        )
        sample_points = ([bps[0] - 1] if bps else [Fraction(0)]) + bps
        starts = [None] + bps
        segments = []
        for point, start in zip(sample_points, starts):
            seg_ab = self._segment_at(point)
            seg_bc = other._segment_at(seg_ab.forward(point))
            segments.append(AffineSegment(
                start,
                seg_ab.rate * seg_bc.rate,
                seg_bc.rate * seg_ab.offset + seg_bc.offset,
            ))
        merged = []
        for seg in segments:
            if merged and merged[-1].rate == seg.rate and merged[-1].offset == seg.offset:
                continue
            merged.append(seg)
        return OffsetTable(self.src_clock, other.dst_clock, merged,
                           version=(self.version, other.version))

    def with_correction(self, at, offset_delta=0, rate=None) -> "OffsetTable":
        """New table (version + 1) with a calibration step from ``at`` onward.

        ``offset_delta`` shifts all later segments; ``rate`` optionally
        replaces the whole tail with a single slewed segment.  Corrections
        that would move time backwards are rejected by validation.
        """
        at = as_fraction(at)
        offset_delta = as_fraction(offset_delta)
        segs = self.segments
        norm = []
        for i, seg in enumerate(segs):
            norm.append(seg)
            dom_end = segs[i + 1].start if i + 1 < len(segs) else None
            if (seg.start is None or seg.start < at) and (dom_end is None or dom_end > at):
                norm.append(AffineSegment(at, seg.rate, seg.offset))
        norm.sort(key=lambda s: (s.start is not None, s.start))
        head = [s for s in norm if s.start is None or s.start < at]
        if rate is not None:
            rate = as_fraction(rate)
            dst_at = self.forward(at) + offset_delta
            tail = [AffineSegment(at, rate, dst_at - rate * at)]
        else:
            tail = [AffineSegment(s.start, s.rate, s.offset + offset_delta)
                    for s in norm if s.start is not None and s.start >= at]
        version = self.version + 1 if isinstance(self.version, int) else self.version
        return OffsetTable(self.src_clock, self.dst_clock, head + tail, version=version)


class Clock:
    """A local clock related to the wall clock by versioned offset tables."""

    def __init__(self, clock_id: str, table: OffsetTable):
        if table.dst_clock != clock_id:
            raise ValueError("table does not target this clock")
        if table.src_clock != WallClockTime.CLOCK_ID:
            raise ValueError("clock tables must be anchored at the wall clock")
        self.clock_id = clock_id
        self._tables = {table.version: table}
        self._latest = table.version

    @property
    def latest_version(self):
        return self._latest

    @property
    def versions(self) -> tuple:
        return tuple(sorted(self._tables))

    def table(self, version=None) -> OffsetTable:
        return self._tables[self._latest if version is None else version]

    def apply_correction(self, at, offset_delta=0, rate=None) -> int:
        """Apply a local calibration correction; returns the new version."""
        new_table = self.table().with_correction(at, offset_delta, rate)
        self._tables[new_table.version] = new_table
        self._latest = new_table.version
        return new_table.version

    def to_local(self, wall_time: WallClockTime, version=None) -> TimePoint:
        return TimePoint(self.clock_id, self.table(version).forward(wall_time.offset))

    def to_wall(self, local_time: TimePoint, version=None) -> WallClockTime:
        if local_time.clock != self.clock_id:
            raise ValueError("time point is on a different clock")
        return WallClockTime(self.table(version).backward(local_time.offset))
