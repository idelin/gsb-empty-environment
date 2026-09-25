"""eqjoin: equi-joins (inner/left/full) on typed record batches.

Standard library only. Hash partitioning with disk spill for small
memory budgets; NULL never matches and is distinct from NaN; duplicate
keys expand with Cartesian multiplicity.
"""
from .batch import RecordBatch
from .core import JoinResult, join
from .types import NULL, ColType, is_nan, is_null

__all__ = [
    "RecordBatch",
    "join",
    "JoinResult",
    "NULL",
    "ColType",
    "is_null",
    "is_nan",
]
