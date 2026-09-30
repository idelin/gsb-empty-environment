"""timecal: exact rational time conversion and clock calibration graphs.

Pure Python 3.11 standard library; no floating point is ever used for
conversion, comparison or calibration arithmetic.
"""

from .rounding import RoundingPolicy, round_fraction
from .units import (
    MICROSECOND,
    MILLISECOND,
    NANOSECOND,
    SECOND,
    TimeUnit,
    compare,
    convert_exact,
    convert_rounded,
    from_seconds,
    to_seconds,
)
from .core import UTC, ClockDomain, ClockKind, Duration, TimePoint, WallClockTime
from .calibration import (
    AffineError,
    CalibratedMap,
    ClockGraph,
    Contradiction,
    GraphView,
    Interval,
    NoPathError,
    PathComparison,
    PathDiscrepancy,
    integer_preimages,
)
from .table import Segment, SyntheticOffsetTable

__all__ = [name for name in dir() if not name.startswith("_")]
