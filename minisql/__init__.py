"""minisql：分区裁剪 + 跳过证明的教学级实现（标准库，无 SQL 引擎）。"""

from .analyzer import Decision, analyze
from .engine import QueryResult, select, select_full_scan
from .logic import Tri
from .partition import (EnumScheme, Interval, KeyEnum, KeyNull, KeyRange,
                        Partition, PartitionedTable, RangeScheme)
from .predicate import And, Compare, Expr, IsNull, Not, Or, Param, normalize

__all__ = [
    "Decision", "analyze", "QueryResult", "select", "select_full_scan", "Tri",
    "EnumScheme", "Interval", "KeyEnum", "KeyNull", "KeyRange", "Partition",
    "PartitionedTable", "RangeScheme",
    "And", "Compare", "Expr", "IsNull", "Not", "Or", "Param", "normalize",
]
