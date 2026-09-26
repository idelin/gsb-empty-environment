"""timemap: exact piecewise-affine mapping between real and logical time."""
from .intervals import Interval, IntervalSet
from .mapping import Evidence, PiecewiseMap, Segment
from .table import OffsetTable, TableEntry
from .types import Duration, LogicalTime, WallClock
from .verify import CompositionCheck, check_composition, rational_samples
from .versioning import MapHistory, Revision

__all__ = [
    "CompositionCheck",
    "Duration",
    "Evidence",
    "Interval",
    "IntervalSet",
    "LogicalTime",
    "MapHistory",
    "OffsetTable",
    "PiecewiseMap",
    "Revision",
    "Segment",
    "TableEntry",
    "WallClock",
    "check_composition",
    "rational_samples",
]
