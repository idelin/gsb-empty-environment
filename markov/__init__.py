"""Sparse Markov chain toolkit (Python 3.11, standard library only)."""

from .sparse import SparseTransitionMatrix, ValidationError
from .chain import AbsorptionReport, MarkovChain, TransientSolver
from .dense import LUSolver, exact_solve
from .iterative import (IterativeResult, PowerResult, assess_convergence,
                        gauss_seidel, power_iteration)

__all__ = [
    "SparseTransitionMatrix",
    "ValidationError",
    "MarkovChain",
    "TransientSolver",
    "AbsorptionReport",
    "LUSolver",
    "exact_solve",
    "IterativeResult",
    "PowerResult",
    "assess_convergence",
    "gauss_seidel",
    "power_iteration",
]
