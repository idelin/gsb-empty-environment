"""pullpipe：纯 Python（标准库）实现的 pull 式有界批次算子管线。"""

from .batch import Batch, EOF
from .errors import CancelledError, OperatorError, PipelineError
from .expr import BinOp, Col, Expr, Lit, and_, col, lit, not_, or_
from .null import Null, and3, cmp3, is_distinct, is_false, is_null, is_true, not3, or3
from .operator import (
    AggDef,
    CancellationToken,
    EmptyInjector,
    Exploder,
    Filter,
    HashAggregate,
    HashJoin,
    Limit,
    Nothing,
    Operator,
    Project,
    Sort,
    TableScan,
    UnionAll,
)
from .pipeline import Pipeline
from .table import CsvTable, GeneratedTable, ListTable, Schema, Table

__all__ = [
    "Batch", "EOF",
    "Null", "and3", "or3", "not3", "cmp3", "is_true", "is_false", "is_null", "is_distinct",
    "PipelineError", "OperatorError", "CancelledError",
    "CancellationToken", "Operator",
    "Table", "ListTable", "CsvTable", "GeneratedTable", "Schema",
    "Expr", "Col", "Lit", "BinOp", "col", "lit", "and_", "or_", "not_",
    "TableScan", "Filter", "Project", "Limit", "HashJoin", "HashAggregate", "AggDef",
    "Sort", "UnionAll", "Nothing", "EmptyInjector", "Exploder",
    "Pipeline",
]
