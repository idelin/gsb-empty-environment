"""timecore: exact rational time arithmetic and multi-clock calibration.

Everything is computed with ``fractions.Fraction``; no float conversion
ever takes place, so comparisons and huge epoch values stay exact.
"""

from .calibration import AffineCalibration
from .errorbudget import ErrorBudget
from .graph import CalibrationGraph, CycleReport
from .quantities import Duration, TimePoint, WallClockTime
from .rounding import RoundingPolicy, round_rational
from .table import ConversionTable
from .units import (
    MICROSECOND,
    MILLISECOND,
    NANOSECOND,
    SAMPLE_44K1,
    SAMPLE_48K,
    SECOND,
    Unit,
    sample_unit,
)

__all__ = [
    "AffineCalibration",
    "CalibrationGraph",
    "ConversionTable",
    "CycleReport",
    "Duration",
    "ErrorBudget",
    "RoundingPolicy",
    "TimePoint",
    "Unit",
    "WallClockTime",
    "round_rational",
    "sample_unit",
    "SECOND",
    "MILLISECOND",
    "MICROSECOND",
    "NANOSECOND",
    "SAMPLE_48K",
    "SAMPLE_44K1",
]
