"""Budgeted maximum coverage with dependencies, conflicts and min-cover.

Pure Python 3.11 standard library; no external solver is used.
"""
from .model import (
    CoverageSet,
    Evaluation,
    Instance,
    Status,
    UnsatisfiableDependency,
)
from .greedy import GreedyResult, greedy
from .exact import ExactResult, brute_force
from .bnb import (
    BnBResult,
    BranchAndBound,
    CheckpointMismatch,
    PruneRecord,
)
from .witness import Witness, make_witness, verify_witness
from .report import GapReport, gap_report

__all__ = [
    "CoverageSet",
    "Evaluation",
    "Instance",
    "Status",
    "UnsatisfiableDependency",
    "GreedyResult",
    "greedy",
    "ExactResult",
    "brute_force",
    "BnBResult",
    "BranchAndBound",
    "CheckpointMismatch",
    "PruneRecord",
    "Witness",
    "make_witness",
    "verify_witness",
    "GapReport",
    "gap_report",
]
