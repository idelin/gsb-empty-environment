"""Synthetic offset-transition tables with bidirectional time conversion.

Three distinct time concepts are modelled and deliberately kept
non-interchangeable:

* :class:`~tztable.core.Instant`   -- a point on the UTC timeline.
* :class:`~tztable.core.WallClock` -- a local date + time-of-day, no offset.
* :class:`~tztable.core.Duration`  -- a fixed amount of elapsed time.
"""

from .core import Duration, Instant, WallClock
from .table import (
    AmbiguousTimeError,
    Candidate,
    GapTimeError,
    IntervalCandidate,
    IntervalMapping,
    LocalGap,
    OffsetPeriod,
    Transition,
    TransitionTable,
    UtcInterval,
    WallReading,
)
from .rules import MonthlyRule, generate_transitions
from .registry import ConversionService, TableRegistry

__all__ = [
    "AmbiguousTimeError",
    "Candidate",
    "ConversionService",
    "Duration",
    "GapTimeError",
    "Instant",
    "IntervalCandidate",
    "IntervalMapping",
    "LocalGap",
    "MonthlyRule",
    "OffsetPeriod",
    "TableRegistry",
    "Transition",
    "TransitionTable",
    "UtcInterval",
    "WallClock",
    "WallReading",
    "generate_transitions",
]
