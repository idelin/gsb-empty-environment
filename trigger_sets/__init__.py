"""Exact common-trigger sets for rational periodic streams.

Public API: exact time model (Duration / TimePoint / WallClockTime /
OffsetTable), versioned rule streams (PeriodicStream), and the lazy
intersection Engine with kth / count / no-solution evidence.
"""

from .checkpoint import Checkpoint, CheckpointMismatch
from .compiler import Segment, StreamConstraint, compile_segments
from .congruence import CrtConflict, ceil_div, common_scale, crt_all, crt_pair
from .engine import Engine, NoSolutionReport, SegmentEvidence
from .streams import PauseInterval, PeriodicStream, PhaseSwitch
from .time_model import (
    SECOND,
    TICK,
    Duration,
    OffsetEntry,
    OffsetTable,
    TimePoint,
    WallClockTime,
)

__all__ = [
    "Checkpoint",
    "CheckpointMismatch",
    "CrtConflict",
    "Duration",
    "Engine",
    "NoSolutionReport",
    "OffsetEntry",
    "OffsetTable",
    "PauseInterval",
    "PeriodicStream",
    "PhaseSwitch",
    "SECOND",
    "Segment",
    "SegmentEvidence",
    "StreamConstraint",
    "TICK",
    "TimePoint",
    "WallClockTime",
    "ceil_div",
    "common_scale",
    "compile_segments",
    "crt_all",
    "crt_pair",
]
