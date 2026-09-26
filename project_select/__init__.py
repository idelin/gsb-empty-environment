"""Budgeted project selection with dependencies and mutual exclusion."""

from .model import Instance, Project
from .solver import (
    SolveResult,
    check_selection,
    is_feasible_selection,
    solve,
    strongly_connected_components,
)

__all__ = [
    "Instance",
    "Project",
    "SolveResult",
    "check_selection",
    "is_feasible_selection",
    "solve",
    "strongly_connected_components",
]
