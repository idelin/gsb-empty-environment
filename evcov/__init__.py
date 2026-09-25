"""Evidence coverage engine.

Computes satisfied intervals and gaps for versioned rules over versioned,
replayable evidence facts. Pure standard library, Python 3.11.
"""

from .model import CategoryRequirement, EvidenceItem, Rule, Source
from .sweep import Coverage, Interval, compute_coverage, covers_horizon
from .subset import SubsetResult, minimal_subset
from .pipeline import Report, replay, run_pipeline

__all__ = [
    "CategoryRequirement",
    "Coverage",
    "EvidenceItem",
    "Interval",
    "Report",
    "Rule",
    "Source",
    "SubsetResult",
    "compute_coverage",
    "covers_horizon",
    "minimal_subset",
    "replay",
    "run_pipeline",
]
