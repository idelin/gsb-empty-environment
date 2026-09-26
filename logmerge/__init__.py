"""Merge multi-source logs with offset uncertainty into a stable causal order."""
from .constraints import Constraint, DifferenceConstraints
from .events import ConflictError, Event, dedupe
from .merger import (
    AdjacentMark,
    CausalCycleError,
    InfeasibleError,
    MergeResult,
    merge,
)
from .timemodel import Duration, OffsetInterval, OffsetTable, TimePoint, WallClock
from .versions import LogMerger, OrderDiff, Version
from .verify import linear_extensions, sampled_orders, verify_markers

__all__ = [
    "AdjacentMark",
    "CausalCycleError",
    "ConflictError",
    "Constraint",
    "DifferenceConstraints",
    "Duration",
    "Event",
    "InfeasibleError",
    "LogMerger",
    "MergeResult",
    "OffsetInterval",
    "OffsetTable",
    "OrderDiff",
    "TimePoint",
    "Version",
    "WallClock",
    "dedupe",
    "linear_extensions",
    "merge",
    "sampled_orders",
    "verify_markers",
]
