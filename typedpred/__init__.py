"""typedpred: typed predicate trees, three-valued logic, block-level pruning.

Pure standard library. No query engines involved.
"""

from .tvl import TVL
from .types import Type, Schema, TypeMismatchError
from .batch import Batch, ColumnStats
from .predicate import (
    Predicate,
    Col,
    Lit,
    Compare,
    IsNull,
    IsNotNull,
    And,
    Or,
    Not,
)
from .engine import (
    PruneDecision,
    QueryResult,
    cross_check,
    prune_decision,
    row_scan,
    select,
)

__all__ = [
    "TVL",
    "Type",
    "Schema",
    "TypeMismatchError",
    "Batch",
    "ColumnStats",
    "Predicate",
    "Col",
    "Lit",
    "Compare",
    "IsNull",
    "IsNotNull",
    "And",
    "Or",
    "Not",
    "PruneDecision",
    "QueryResult",
    "prune_decision",
    "row_scan",
    "select",
    "cross_check",
]
