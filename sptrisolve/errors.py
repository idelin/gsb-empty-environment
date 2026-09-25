"""Exception hierarchy for sptrisolve."""


class TriangularError(Exception):
    """Base class for all sptrisolve errors."""


class StructureError(TriangularError, ValueError):
    """Invalid matrix structure (dimensions, indices, triangularity)."""


class DimensionError(StructureError):
    """Inconsistent sizes, index types or vector dimensions."""


class NotTriangularError(StructureError):
    """An entry lies outside the declared triangle."""


class MissingPivotError(StructureError):
    """A required diagonal entry is absent from the structure (structural)."""


class ZeroPivotError(TriangularError, ArithmeticError):
    """A numerically zero pivot was hit during substitution (numerical)."""
