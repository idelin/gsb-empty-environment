"""Partition UTC windows by local calendar boundaries.

Given an offset table, a UTC window (half-open, exact Fraction endpoints)
and one or more local calendars, :func:`partition_window` builds the *common
refinement* of all the calendar partitions:

* every local boundary wall-time is converted to **all** of its UTC
  preimages (a boundary can occur zero, one, or several times);
* every offset transition inside the window is also a cut point, so labels
  are constant on each elementary piece;
* elementary pieces sharing the exact same label tuple are merged into one
  :class:`Segment` -- therefore a segment may own **several disjoint UTC
  intervals** (e.g. a local date that occurred twice).  We never assume a
  day has a single continuous preimage.

Coverage conservation is checked by :func:`verify_coverage` and, as an
independent reference, by :func:`reference_verify`, which re-cuts the window
at every transition point and every segment endpoint.

All enumeration is jump-based (boundary to boundary, transition to
transition); a :class:`WorkCounter` records the effort so extremely long
windows are handled without per-second scanning.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Dict, List, Optional, Sequence, Tuple

from .calendars import iter_boundaries
from .exact import SECONDS_PER_DAY, Duration, Interval, WallTime
from .offsets import OffsetTable


@dataclass
class WorkCounter:
    """Jump-based effort counters (never counts seconds)."""

    boundary_jumps: int = 0
    transition_visits: int = 0
    wall_lookups: int = 0

    def total(self) -> int:
        return self.boundary_jumps + self.transition_visits + self.wall_lookups


class Segment:
    """Maximal set of UTC intervals sharing one label tuple."""

    def __init__(self, labels: Dict[str, str]):
        self.labels = dict(labels)
        self.intervals: List[Interval] = []

    @property
    def duration(self) -> Duration:
        total = Fraction(0)
        for iv in self.intervals:
            total += iv.length
        return Duration(total)

    def _append(self, iv: Interval) -> None:
        if self.intervals and self.intervals[-1].end == iv.start:
            prev = self.intervals[-1]
            self.intervals[-1] = Interval(prev.start, iv.end)
        else:
            self.intervals.append(iv)

    def __repr__(self):
        return f"Segment(labels={self.labels!r}, intervals={self.intervals!r})"


def partition_window(
    table: OffsetTable,
    window: Interval,
    calendars: Sequence,
    work: Optional[WorkCounter] = None,
) -> List[Segment]:
    """Build the common refinement of ``calendars`` over ``window``.

    Returns segments sorted by their first UTC interval; each segment carries
    its label in every calendar plus all of its (possibly disjoint) UTC
    preimage intervals.
    """
    if work is None:
        work = WorkCounter()
    if window.is_empty:
        return []

    cuts = {window.start, window.end}

    # Cut at every offset transition inside the window.
    for tr in table.transitions_between(window.start, window.end):
        work.transition_visits += 1
        cuts.add(tr.at)

    # Cut at every UTC preimage of every local boundary.  The search range is
    # padded by the offset span so no preimage can be missed.
    lo_off, hi_off = table.offset_bounds()
    pad = (hi_off - lo_off) + max(abs(lo_off), abs(hi_off)) + 2 * SECONDS_PER_DAY
    wall_lo = table.instant_to_wall(window.start)
    wall_hi = table.instant_to_wall(window.end)
    enum_lo = WallTime.from_naive_seconds(wall_lo.to_naive_seconds() - pad)
    enum_hi = WallTime.from_naive_seconds(wall_hi.to_naive_seconds() + pad)
    for cal in calendars:
        for boundary in iter_boundaries(cal, enum_lo, enum_hi, work):
            for t in table.wall_to_instants(boundary):
                work.wall_lookups += 1
                if window.start < t < window.end:
                    cuts.add(t)

    ordered = sorted(cuts)
    segments: Dict[Tuple[str, ...], Segment] = {}
    for a, b in zip(ordered, ordered[1:]):
        if a >= b:
            continue
        mid = (a + b) / 2
        wall = table.instant_to_wall(mid)
        labels = {cal.name: cal.label(wall) for cal in calendars}
        key = tuple(labels[cal.name] for cal in calendars)
        seg = segments.get(key)
        if seg is None:
            seg = Segment(labels)
            segments[key] = seg
        seg._append(Interval(a, b))
    return sorted(segments.values(), key=lambda s: s.intervals[0].start)


@dataclass
class CoverageReport:
    ok: bool
    problems: List[str] = field(default_factory=list)
    total_duration: Duration = field(default_factory=Duration)


def verify_coverage(segments: Sequence[Segment], window: Interval) -> CoverageReport:
    """Check the segment intervals tile ``window`` exactly: no gaps, no overlaps."""
    problems: List[str] = []
    intervals = sorted(
        (iv for seg in segments for iv in seg.intervals),
        key=lambda iv: (iv.start, iv.end),
    )
    for prev, cur in zip(intervals, intervals[1:]):
        if cur.start < prev.end:
            problems.append(f"overlap: {prev!r} vs {cur!r}")
    cursor = window.start
    for iv in intervals:
        if iv.start != cursor:
            problems.append(f"gap or out-of-window start at {iv.start}, expected {cursor}")
        cursor = iv.end
    if cursor != window.end:
        problems.append(f"union ends at {cursor}, window ends at {window.end}")
    total = sum((iv.length for iv in intervals), Fraction(0))
    if total != window.length:
        problems.append(f"measure mismatch: {total} != {window.length}")
    return CoverageReport(ok=not problems, problems=problems, total_duration=Duration(total))


def reference_verify(table: OffsetTable, window: Interval, segments: Sequence[Segment]) -> bool:
    """Independent check: re-cut at every transition point and segment endpoint.

    Every elementary piece of that reference cut must lie inside exactly one
    segment interval, and the total measure must equal the window length.
    """
    cuts = {window.start, window.end}
    for tr in table.transitions_between(window.start, window.end):
        cuts.add(tr.at)
    for seg in segments:
        for iv in seg.intervals:
            cuts.add(iv.start)
            cuts.add(iv.end)
    ordered = sorted(cuts)
    flat = [iv for seg in segments for iv in seg.intervals]
    for a, b in zip(ordered, ordered[1:]):
        if a >= b:
            continue
        mid = (a + b) / 2
        hits = sum(1 for iv in flat if iv.start <= mid < iv.end)
        if hits != 1:
            return False
    total = sum((iv.length for iv in flat), Fraction(0))
    return total == window.length
