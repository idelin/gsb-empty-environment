"""Dependency-scheduled sparse triangular solver (pure stdlib, Python 3.11)."""

from .errors import (
    DimensionError,
    MissingPivotError,
    StructureError,
    TriSolverError,
    ZeroPivotError,
)
from .matrix import LOWER, UPPER, Entry, TriangularMatrix
from .solver import SolveStats, TriangularSolver
from .analysis import Dependency, LevelSchedule, analyze

__all__ = [
    "LOWER",
    "UPPER",
    "Entry",
    "TriangularMatrix",
    "TriangularSolver",
    "SolveStats",
    "Dependency",
    "LevelSchedule",
    "analyze",
    "TriSolverError",
    "StructureError",
    "MissingPivotError",
    "ZeroPivotError",
    "DimensionError",
]
