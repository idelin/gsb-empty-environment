"""vector_expr: compile expressions to vectorized execution plans.

Public API:

* :func:`compile_expression` - parse + type-check an expression string
  against a schema, producing a :class:`~vector_expr.plan.Plan`.
* :class:`Batch` / :class:`Column` - typed columnar input.
* :class:`Type` - INT64 / FLOAT64 / BOOL.
"""

from .batch import Batch, Column
from .compiler import compile_expression
from .errors import (
    CastError,
    CompileError,
    DivisionByZeroError,
    EvaluationError,
    IntegerOverflowError,
)
from .plan import Plan
from .types import INT64_MAX, INT64_MIN, Type

__all__ = [
    "Batch",
    "CastError",
    "Column",
    "CompileError",
    "DivisionByZeroError",
    "EvaluationError",
    "INT64_MAX",
    "INT64_MIN",
    "IntegerOverflowError",
    "Plan",
    "Type",
    "compile_expression",
]
