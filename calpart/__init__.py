"""calpart: exact partitioning of UTC windows by local calendar boundaries.

Concepts (kept strictly separate):

* wall-clock time  -- :class:`calpart.exact.WallTime` (naive local reading)
* instant          -- :class:`calpart.exact.Instant` (point on UTC timeline)
* duration         -- :class:`calpart.exact.Duration` (exact length of time)
"""

from .calendars import DayCalendar, MonthCalendar, WeekCalendar, iter_boundaries
from .exact import Duration, Instant, Interval, WallTime
from .offsets import OffsetTable, Transition
from .partition import (
    CoverageReport,
    Segment,
    WorkCounter,
    partition_window,
    reference_verify,
    verify_coverage,
)

__all__ = [
    "CoverageReport",
    "DayCalendar",
    "Duration",
    "Instant",
    "Interval",
    "MonthCalendar",
    "OffsetTable",
    "Segment",
    "Transition",
    "WallTime",
    "WeekCalendar",
    "WorkCounter",
    "iter_boundaries",
    "partition_window",
    "reference_verify",
    "verify_coverage",
]
