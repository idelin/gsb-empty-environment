"""Out-of-order wrap-around counter analysis.

Distinguishes counter wrap from device reset, judges per-interval deltas
as definite/undecidable from per-source bit width and max decidable
sampling interval, and supports incremental revision under late inserts
and retractions with precise invalidation reporting.
"""

from .delta import IntervalResult, Reason, judge_pair
from .engine import IncrementalEngine, Revision, SourceTotal
from .model import CounterConfig, Sample, SourceConfig
from .reference import ReferenceState, recompute

__all__ = [
    "CounterConfig",
    "IncrementalEngine",
    "IntervalResult",
    "Reason",
    "ReferenceState",
    "Revision",
    "Sample",
    "SourceConfig",
    "SourceTotal",
    "judge_pair",
    "recompute",
]
