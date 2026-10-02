"""Sparse PCG with true-residual convergence, hot starts and checkpoints."""

from .matrix import SparseMatrix
from .preconditioner import DiagonalPreconditioner
from .solver import (
    CGResult,
    CGSolver,
    CGStatus,
    Checkpoint,
    VersionMismatchError,
)
from .dense import dense_solve

__all__ = [
    "SparseMatrix",
    "DiagonalPreconditioner",
    "CGSolver",
    "CGResult",
    "CGStatus",
    "Checkpoint",
    "VersionMismatchError",
    "dense_solve",
]
