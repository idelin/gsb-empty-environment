"""lexopt: finite-domain constraint optimization with lexicographic objectives.

- Hard constraints are always enforced; soft constraints add integer penalties.
- Objectives are minimized lexicographically across hierarchy levels.
- Negative/zero soft weights are rejected at model construction time.
- An independent checker re-validates claimed solutions against the
  original constraints.
"""

from .model import Constraint, Model, Variable
from .constraints import all_different, linear_constraint
from .solver import (
    FEASIBLE,
    INFEASIBLE,
    OPTIMAL,
    UNKNOWN,
    SolveResult,
    Solver,
    Violation,
)
from .checker import CheckReport, check_solution, verify_result
from .explain import explain

__all__ = [
    "Constraint",
    "Model",
    "Variable",
    "all_different",
    "linear_constraint",
    "Solver",
    "SolveResult",
    "Violation",
    "OPTIMAL",
    "FEASIBLE",
    "INFEASIBLE",
    "UNKNOWN",
    "CheckReport",
    "check_solution",
    "verify_result",
    "explain",
]
