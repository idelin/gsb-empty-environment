"""Multi-version temporal evidence selection.

Selects evidence sets that are simultaneously visible at one observation
time and whose business validity intervals overlap, subject to source
mutual-exclusion rules. Returns all maximal feasible time segments with at
least one consistent witness selection each, or explicit inconsistency
reasons. Supports incremental undo on fact updates, budget-bounded search
with explicit unknown segments, and replayable evidence journals.
"""

from .model import FactVersion, MutexRule, Query, common_interval, is_consistent, visible_versions
from .solver import (
    EMPTY_INTERSECTION,
    FEASIBLE,
    INFEASIBLE,
    MUTEX_CONFLICT,
    NOT_VISIBLE,
    NO_COVERAGE,
    SEARCH_INCOMPLETE,
    UNKNOWN,
    Reason,
    SegmentResult,
    Selection,
    SolveResult,
    segment_signature,
    solve,
)
from .bruteforce import exhaustive_feasible_intervals
from .incremental import IncrementalEngine, UpdateReport
from .replay import replay_solve

__all__ = [
    "FactVersion",
    "MutexRule",
    "Query",
    "common_interval",
    "is_consistent",
    "visible_versions",
    "solve",
    "SolveResult",
    "SegmentResult",
    "Selection",
    "Reason",
    "segment_signature",
    "exhaustive_feasible_intervals",
    "IncrementalEngine",
    "UpdateReport",
    "replay_solve",
    "FEASIBLE",
    "INFEASIBLE",
    "UNKNOWN",
    "NO_COVERAGE",
    "NOT_VISIBLE",
    "MUTEX_CONFLICT",
    "EMPTY_INTERSECTION",
    "SEARCH_INCOMPLETE",
]
