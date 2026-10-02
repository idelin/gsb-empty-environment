"""Sparse CG with updatable diagonal preconditioning (standard library only)."""

from .matrix import (
    DimensionError,
    MatrixFormatError,
    SparseMatrix,
    SymmetryReport,
)
from .preconditioner import DiagonalPreconditioner, ZeroDiagonalError
from .solver import (
    CGResult,
    CGSolver,
    MatrixVersionError,
    STATUS_ASYMMETRIC,
    STATUS_CANCELLED,
    STATUS_CONVERGED,
    STATUS_INDEFINITE,
    STATUS_INDEFINITE_PRECONDITIONER,
    STATUS_MAX_ITER,
    STATUS_STAGNATED,
)

__all__ = [
    "CGResult",
    "CGSolver",
    "DiagonalPreconditioner",
    "DimensionError",
    "MatrixFormatError",
    "MatrixVersionError",
    "SparseMatrix",
    "SymmetryReport",
    "ZeroDiagonalError",
    "STATUS_ASYMMETRIC",
    "STATUS_CANCELLED",
    "STATUS_CONVERGED",
    "STATUS_INDEFINITE",
    "STATUS_INDEFINITE_PRECONDITIONER",
    "STATUS_MAX_ITER",
    "STATUS_STAGNATED",
]
