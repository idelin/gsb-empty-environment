"""Transactional, exact-rational unit conversion graph.

Public API:

* :class:`VersionedGraph` -- in-memory versioned conversion graph.
* :class:`GraphState` -- one committed graph version.
* Exceptions in :mod:`unitconv.core`.
* :func:`unitconv.store.load_store` / :func:`unitconv.store.append_batch`
  for JSON persistence.
"""

from .core import (
    ConversionResult,
    ContradictionError,
    DimensionConflictError,
    DimensionMismatchError,
    GraphState,
    NoConversionPathError,
    StatementError,
    UnknownUnitError,
    VersionedGraph,
    format_factor,
    parse_factor,
    parse_statement,
)

__all__ = [
    "ConversionResult",
    "ContradictionError",
    "DimensionConflictError",
    "DimensionMismatchError",
    "GraphState",
    "NoConversionPathError",
    "StatementError",
    "UnknownUnitError",
    "VersionedGraph",
    "format_factor",
    "parse_factor",
    "parse_statement",
]

__version__ = "1.0.0"
