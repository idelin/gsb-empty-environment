"""sptrisolve: sparse triangular solves with dependency-level scheduling.

Pure Python 3.11, standard library only.  Supports lower/upper triangular
matrices, unit diagonals, transpose views, multiple right-hand sides sharing
one compiled analysis, and incremental (partial) re-analysis after structural
changes.  Missing and zero pivots are distinct, explicitly reported errors.
"""

from .analysis import SolveAnalysis
from .errors import (DimensionError, MissingPivotError, NotTriangularError,
                     StructureError, TriangularError, ZeroPivotError)
from .matrix import TransposeView, TriangularMatrix

__all__ = [
    "DimensionError",
    "MissingPivotError",
    "NotTriangularError",
    "SolveAnalysis",
    "StructureError",
    "TransposeView",
    "TriangularError",
    "TriangularMatrix",
    "ZeroPivotError",
]

__version__ = "0.1.0"
