"""vecplan: a tiny vectorized expression engine.

Compiles arithmetic, comparison, conditional and explicit-cast expressions
into vector execution plans. Type errors are reported at compile time,
before any data is touched. No external query engine is used.
"""
from .batch import RecordBatch
from .compiler import compile
from .errors import CompileError, ErrorPolicy, ExecutionError, VecPlanError
from .expr import and_, cast, col, if_, is_null, lit, not_, or_
from .plan import Vector
from .types import BOOL, FLOAT64, INT32, INT64, STRING, Type

__all__ = [
    "BOOL", "FLOAT64", "INT32", "INT64", "STRING", "Type",
    "CompileError", "ErrorPolicy", "ExecutionError", "RecordBatch",
    "VecPlanError", "Vector",
    "and_", "cast", "col", "compile", "if_", "is_null", "lit", "not_", "or_",
]
