"""Asymmetric capacitated VRP with time windows on synthetic exact data.

Pure standard library; no solver calls, no map integration.
"""

from .model import Customer, Problem, Result, Status, as_fraction
from .exact import solve_exact
from .heuristic import solve_heuristic
from .verify import verify_routes, format_verification

__all__ = [
    "Customer", "Problem", "Result", "Status", "as_fraction",
    "solve_exact", "solve_heuristic", "verify_routes", "format_verification",
]
