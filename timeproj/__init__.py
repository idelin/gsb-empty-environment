"""Exact index projection from continuous time intervals onto piecewise
sampled sequences (standard library only)."""

from .blocks import SamplingBlock
from .calibration import (
    CalibratedProjector,
    CalibrationStore,
    Cursor,
    OffsetTable,
)
from .compose import compose, project_slices
from .projection import (
    DedupeEvent,
    Gap,
    IndexSlice,
    MissingSample,
    ProjectionResult,
    ReverseCoverage,
    RoundingEvidence,
    project,
)
from .types import Duration, Interval, TimePoint, WallClockTime

__all__ = [
    "CalibratedProjector",
    "CalibrationStore",
    "Cursor",
    "DedupeEvent",
    "Duration",
    "Gap",
    "IndexSlice",
    "Interval",
    "MissingSample",
    "OffsetTable",
    "ProjectionResult",
    "ReverseCoverage",
    "RoundingEvidence",
    "SamplingBlock",
    "TimePoint",
    "WallClockTime",
    "compose",
    "project",
    "project_slices",
]
