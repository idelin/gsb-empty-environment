"""trigsync：有理周期/相位触发流的精确共同触发集合。"""

from .crt import Conflict, Congruence, NoCommonSolution, crt_all, crt_pair
from .engine import Checkpoint, Intersection, SegmentReport
from .streams import ExceptionRule, PauseRule, PhaseRule, Stream
from .timebase import (
    Duration,
    Instant,
    OffsetEntry,
    OffsetTable,
    TickScale,
    WallClock,
)

__all__ = [
    "Checkpoint",
    "Conflict",
    "Congruence",
    "Duration",
    "ExceptionRule",
    "Instant",
    "Intersection",
    "NoCommonSolution",
    "OffsetEntry",
    "OffsetTable",
    "PauseRule",
    "PhaseRule",
    "SegmentReport",
    "Stream",
    "TickScale",
    "WallClock",
    "crt_all",
    "crt_pair",
]
