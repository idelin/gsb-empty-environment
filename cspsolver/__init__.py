"""A tiny finite-domain constraint solver with lexicographic soft objectives.

Design guarantees:
- Hard constraints are never relaxed; no penalty can buy through a hard conflict.
- Soft constraints carry non-negative integer weights and a priority level.
  Objectives are optimized lexicographically by level (level 0 first), so a
  high-level penalty can never be offset by any amount of low-level gain.
- Negative weights are rejected at model construction time.
- Every claimed solution is re-verified by an independent checker
  (cspsolver.checker) against the original constraints before being returned.
"""

from .model import HardConstraint, Model, SoftConstraint, Variable
from .solver import PenaltyTerm, LevelSummary, SolveResult, Solver, Status
from .checker import CheckResult, Violation, check_assignment

__all__ = [
    "HardConstraint",
    "SoftConstraint",
    "Variable",
    "Model",
    "Solver",
    "SolveResult",
    "Status",
    "PenaltyTerm",
    "LevelSummary",
    "CheckResult",
    "Violation",
    "check_assignment",
]
