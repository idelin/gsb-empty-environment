"""vtline -- versioned valid-time timelines with minimal reversible diffs."""

from .time_model import (
    AmbiguousWallTimeError,
    Duration,
    Instant,
    InvalidWallTimeError,
    OffsetTable,
    WallClock,
)
from .model import ChangeKind, ChangeSpan, Diff, Entry, Segment, classify_change
from .timeline import ApplyStats, BaselineMismatchError, Summary, Timeline
from .diff import compose, diff, inverse
from .merge import Conflict, MergeResult, RebaseResult, merge_three_way, rebase
from .repository import Repository
from .verify import endpoints_of, full_scan_equal, sample_points

__all__ = [
    "AmbiguousWallTimeError",
    "ApplyStats",
    "BaselineMismatchError",
    "ChangeKind",
    "ChangeSpan",
    "compose",
    "Conflict",
    "Diff",
    "diff",
    "Duration",
    "endpoints_of",
    "Entry",
    "full_scan_equal",
    "Instant",
    "InvalidWallTimeError",
    "inverse",
    "merge_three_way",
    "MergeResult",
    "OffsetTable",
    "rebase",
    "RebaseResult",
    "Repository",
    "sample_points",
    "Segment",
    "classify_change",
    "Summary",
    "Timeline",
    "WallClock",
]
