"""Fault-domain-aware replica placement with exact rational arithmetic."""

from .migrate import Step, apply_steps, build_migration_plan
from .model import Problem, placement_cost, to_fraction, validate_placement
from .solver import SolveResult, Status, solve

__all__ = [
    "Problem",
    "SolveResult",
    "Status",
    "Step",
    "apply_steps",
    "build_migration_plan",
    "placement_cost",
    "solve",
    "to_fraction",
    "validate_placement",
]
