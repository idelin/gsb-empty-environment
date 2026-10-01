"""symbreak: verified symmetry breaking for small finite-domain CSPs.

Pure Python 3.11 standard library; no SAT/SMT solvers involved.
"""

from .core import Constraint, Problem
from .groups import BudgetExceeded, closure
from .permutations import (
    Counterexample,
    apply_perm,
    compose,
    identity,
    inverse,
    normalize,
    verify_automorphism,
)
from .recovery import recover_symmetric
from .solver import check_original, optimize, solve_all, solve_one
from .symmetry import (
    SymmetryRejected,
    add_symmetry_generators,
    declare_exchangeable,
    merge_groups,
)
from .verify import VerificationReport, compute_orbits, verify_small

__all__ = [
    "BudgetExceeded",
    "Constraint",
    "Counterexample",
    "Problem",
    "SymmetryRejected",
    "VerificationReport",
    "add_symmetry_generators",
    "apply_perm",
    "check_original",
    "closure",
    "compose",
    "compute_orbits",
    "declare_exchangeable",
    "identity",
    "inverse",
    "merge_groups",
    "normalize",
    "optimize",
    "recover_symmetric",
    "solve_all",
    "solve_one",
    "verify_automorphism",
    "verify_small",
]
