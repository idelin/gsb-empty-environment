"""Multi-layer position mapping: source -> macro expansion -> bytecode.

Mappings are interval relations (sets of blocks), not single offsets, so
one-to-many fragments, overlapping sources and generated fragments survive
composition.  Everything runs on Python 3.11 stdlib only.
"""

from .intervals import Block, Interval, IntervalMap
from .pipeline import (
    Candidate,
    Diagnosis,
    Diagnostic,
    Pipeline,
    UpdateReport,
    Version,
)

__all__ = [
    "Block",
    "Candidate",
    "Diagnosis",
    "Diagnostic",
    "Interval",
    "IntervalMap",
    "Pipeline",
    "UpdateReport",
    "Version",
]
