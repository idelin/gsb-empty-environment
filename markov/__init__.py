"""Sparse Markov chain toolkit (pure standard library).

- Explicit size and indices; missing lines and bad line sums are errors,
  never silently repaired.
- Row or column storage convention, unified internally.
- Communicating-class decomposition, closed-class and absorbing-state
  detection, irreducibility diagnostics.
- Absorption probabilities and expected absorption steps (dense LU, sparse
  SOR with true-residual certification, exact rational reference).
- Distribution propagation with per-step mass-conservation error.
"""

from .chain import (
    AbsorptionResult,
    MarkovChain,
    PropagationRecord,
    StationaryResult,
)
from .classify import ChainStructure, ClosedClass
from .errors import (
    DistributionError,
    IndexOutOfRangeError,
    LineSumError,
    MarkovError,
    MissingLineError,
    NegativeProbabilityError,
    NotConvergedError,
    SingularMatrixError,
    ValidationError,
)
from .solvers import IterativeResult, LUSolver, assess_convergence
from .sparse import Convention, SparseTransitionMatrix

__all__ = [
    "AbsorptionResult",
    "ChainStructure",
    "ClosedClass",
    "Convention",
    "DistributionError",
    "IndexOutOfRangeError",
    "IterativeResult",
    "LUSolver",
    "LineSumError",
    "MarkovChain",
    "MarkovError",
    "MissingLineError",
    "NegativeProbabilityError",
    "NotConvergedError",
    "PropagationRecord",
    "SingularMatrixError",
    "SparseTransitionMatrix",
    "StationaryResult",
    "ValidationError",
    "assess_convergence",
]

__version__ = "0.1.0"
