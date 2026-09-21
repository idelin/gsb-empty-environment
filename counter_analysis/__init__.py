"""Out-of-order counter increment analysis.

Explains raw counter readings as wraps, explicit resets, or genuinely
unknowable gaps, and keeps downstream consumers informed about exactly
which interval results each late insert / retraction voids.
"""

from .model import Diff, DiffStatus, Sample, SourceConfig, compute_diff
from .reference import reference_diffs
from .store import CounterStore, Revision, SourceTotals

__all__ = [
    "CounterStore",
    "Diff",
    "DiffStatus",
    "Revision",
    "Sample",
    "SourceConfig",
    "SourceTotals",
    "compute_diff",
    "reference_diffs",
]

__version__ = "0.1.0"
