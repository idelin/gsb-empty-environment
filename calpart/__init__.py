"""calpart: partition UTC windows into local calendar periods, exactly.

Core ideas:
- ``Instant`` / ``Wall`` / ``Duration`` keep time points, wall-clock readings
  and elapsed lengths type-safe apart, all exact (``Fraction`` seconds).
- ``OffsetTable`` is a piecewise-constant UTC offset with local revision.
- ``DayGrid`` / ``WeekGrid`` / ``MonthGrid`` define local period boundaries.
- ``iter_segments`` lazily partitions a UTC window; ``SegmentPager`` pages it.
- ``common_refinement`` overlays several calendars into one atomic partition
  with per-label UTC preimages (a label may own several disjoint intervals).
- ``verify`` checks coverage conservation, including jump-based scans of
  extremely long windows.
"""
from .exact import (
    SECONDS_PER_DAY,
    Duration,
    Instant,
    Wall,
    as_fraction,
    format_civil,
)
from .grids import DayGrid, MonthGrid, WeekGrid
from .offsets import OffsetTable
from .partition import Segment, SegmentPager, iter_segments, local_range_attained, partition
from .refine import CalendarSpec, Refinement, RefinedSegment, common_refinement
from .verify import (
    WorkStats,
    cut_at_transitions,
    jump_scan,
    verify_piecewise_conservation,
    verify_segments_cover,
)

__all__ = [
    "SECONDS_PER_DAY",
    "Duration",
    "Instant",
    "Wall",
    "as_fraction",
    "format_civil",
    "DayGrid",
    "MonthGrid",
    "WeekGrid",
    "OffsetTable",
    "Segment",
    "SegmentPager",
    "iter_segments",
    "local_range_attained",
    "partition",
    "CalendarSpec",
    "Refinement",
    "RefinedSegment",
    "common_refinement",
    "WorkStats",
    "cut_at_transitions",
    "jump_scan",
    "verify_piecewise_conservation",
    "verify_segments_cover",
]
