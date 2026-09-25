"""clockmap: recover wall-clock time from sample indices and calibrations.

Exact rational arithmetic only (``fractions.Fraction``); floats are refused
at the API boundary so nearby large timestamps are never merged.
"""

from .mapper import (
    EpochMapper,
    PiecewiseClockMapper,
    QueryRecord,
    Revision,
    TimeInterval,
    find_drops,
)
from .model import Anchor, Bound, Line, Segment, SlopeBounds
from .reference import check_epochs_independent, reference_band, reference_feasible
from .timetypes import Duration, WallClock

__all__ = [
    "Anchor",
    "Bound",
    "Duration",
    "EpochMapper",
    "Line",
    "PiecewiseClockMapper",
    "QueryRecord",
    "Revision",
    "Segment",
    "SlopeBounds",
    "TimeInterval",
    "WallClock",
    "check_epochs_independent",
    "find_drops",
    "reference_band",
    "reference_feasible",
]
