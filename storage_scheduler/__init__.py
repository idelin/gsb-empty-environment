"""Exact storage charge/discharge scheduling (stdlib only, no solvers)."""

from .model import Instance, to_fraction
from .solver import (
    STATUS_FEASIBLE,
    STATUS_INFEASIBLE,
    STATUS_OPTIMAL,
    STATUS_UNDECIDED,
    PeriodSchedule,
    SolveResult,
    objective_of,
    solve,
    validate_schedule,
)

__all__ = [
    "Instance",
    "to_fraction",
    "solve",
    "validate_schedule",
    "objective_of",
    "PeriodSchedule",
    "SolveResult",
    "STATUS_OPTIMAL",
    "STATUS_FEASIBLE",
    "STATUS_INFEASIBLE",
    "STATUS_UNDECIDED",
]
