"""Subset-minimal vs minimum-cardinality conflict cores over finite domains.

Pure standard library. No SAT/SMT solver is used: an internal brute-force
backtracking solver with an explicit node budget answers SAT / UNSAT /
UNKNOWN, and UNKNOWN (budget exhausted) is propagated as undecided, never
treated as SAT or UNSAT.
"""

from .solver import Constraint, Problem, SolveResult, Status, solve
from .checker import check_assignment
from .cache import CachedSolver, InternalSolverError
from .core import (
    CheckRecord,
    CoreResult,
    deletion_based_core,
    minimum_cardinality_core,
)
from .enumeration import CoreVerification, enumerate_all_subsets, verify_core

__all__ = [
    "CachedSolver",
    "CheckRecord",
    "Constraint",
    "CoreResult",
    "CoreVerification",
    "InternalSolverError",
    "Problem",
    "SolveResult",
    "Status",
    "check_assignment",
    "deletion_based_core",
    "enumerate_all_subsets",
    "minimum_cardinality_core",
    "solve",
    "verify_core",
]
