"""Conservative interval analysis with exact enumeration cross-check.

Pure Python 3.11 standard library. All bundled examples use synthetic data.
"""

from .interval import Interval
from .parser import parse, ParseError
from .checker import (
    AnalysisResult,
    Finding,
    CheckStatus,
    analyze,
    analyze_dict,
)

__all__ = [
    "Interval",
    "parse",
    "ParseError",
    "AnalysisResult",
    "Finding",
    "CheckStatus",
    "analyze",
    "analyze_dict",
]
