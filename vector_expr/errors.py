"""Error hierarchy for the vector expression engine.

Two phases, two error families:

* Compile time: :class:`CompileError` (raised before any data is touched).
* Execution time: :class:`EvaluationError` and subclasses, which always
  carry the 0-based row index that triggered the failure.
"""

from __future__ import annotations


class CompileError(Exception):
    """Raised while parsing or type-checking an expression.

    Nothing is executed when this is raised: type errors are reported
    before the plan ever sees a batch.
    """


class EvaluationError(Exception):
    """Base class for runtime (per-row) evaluation failures."""

    def __init__(self, row: int, message: str):
        self.row = row
        super().__init__(f"row {row}: {message}")


class IntegerOverflowError(EvaluationError):
    """An INT64 computation produced a value outside the INT64 range."""


class DivisionByZeroError(EvaluationError):
    """Integer division or modulo with a zero divisor."""


class CastError(EvaluationError):
    """An explicit CAST failed for a concrete row (e.g. NaN -> INT64)."""
