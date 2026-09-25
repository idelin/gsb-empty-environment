"""Error types and the execution error policy."""
from __future__ import annotations

import enum


class VecPlanError(Exception):
    """Base class for all engine errors."""


class CompileError(VecPlanError):
    """Raised while type-checking an expression, before any execution."""


class ExecutionError(VecPlanError):
    """Raised when evaluation fails under the STRICT error policy.

    ``rows`` carries the absolute batch row indices that failed.
    """

    def __init__(self, message, rows=()):
        super().__init__(message)
        self.rows = list(rows)


class ErrorPolicy(enum.Enum):
    #: Raise ExecutionError, reporting every offending row.
    STRICT = "strict"
    #: Convert offending values to null and keep going.
    NULL = "null"
