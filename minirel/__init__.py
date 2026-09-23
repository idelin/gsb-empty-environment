"""minirel: a tiny in-process relational executor with shared budgets,
explicit NULL/multiset semantics, limit-vs-budget distinction and
cancellation-safe temp resources. Standard library only."""

from .budget import Budget, BudgetExhausted, BudgetTracker, Cancelled
from .executor import Executor, QueryStats, Result
from .operators import (
    Aggregate,
    Filter,
    HashJoin,
    Limit,
    PartialAggregate,
    Project,
    Scan,
    SharedConsumer,
    SharedSubplan,
    Sort,
)
from .table import NULL, Table, is_null, sql_and, sql_eq, sql_not, sql_or

__all__ = [
    "Aggregate", "Budget", "BudgetExhausted", "BudgetTracker", "Cancelled",
    "Executor", "Filter", "HashJoin", "Limit", "NULL", "PartialAggregate",
    "Project", "QueryStats", "Result", "Scan", "SharedConsumer",
    "SharedSubplan", "Sort", "Table", "is_null", "sql_and", "sql_eq",
    "sql_not", "sql_or",
]
