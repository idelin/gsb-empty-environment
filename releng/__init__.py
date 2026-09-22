"""releng：纯标准库的内存关系执行引擎（Python 3.11+）。

特性：行数/内存/步骤三类共享预算、部分结果与未完成标记、
LIMIT 与预算中断的区分、重复子计划记账、取消与临时资源释放、
NULL 三值逻辑与多重集合语义。不调用任何 SQL 引擎。
"""
from .core import (
    Budget,
    BudgetExhausted,
    ExecutionContext,
    QueryCancelled,
    ResourceRegistry,
    TerminationReason,
    sql_eq,
    sql_ge,
    sql_gt,
    sql_le,
    sql_lt,
)
from .executor import QueryResult, Stats, execute
from .operators import (
    Filter,
    HashAggregate,
    HashJoin,
    Limit,
    NestedLoopJoin,
    Operator,
    Project,
    Scan,
    Sort,
)
from .tables import Table, make_bulk_table, orders, users

__all__ = [
    "Budget",
    "BudgetExhausted",
    "ExecutionContext",
    "QueryCancelled",
    "ResourceRegistry",
    "TerminationReason",
    "sql_eq",
    "sql_ge",
    "sql_gt",
    "sql_le",
    "sql_lt",
    "QueryResult",
    "Stats",
    "execute",
    "Filter",
    "HashAggregate",
    "HashJoin",
    "Limit",
    "NestedLoopJoin",
    "Operator",
    "Project",
    "Scan",
    "Sort",
    "Table",
    "make_bulk_table",
    "orders",
    "users",
]
