"""Multi-table join planning and execution without a SQL engine.

- Inner joins commute freely; outer joins and correlated dependencies
  constrain the legal join orders.
- Cost model accounts for intermediate rows, memory footprint and spill,
  not a single selectivity product.
- Statistics may be unknown and are interval-valued; the planner then
  returns conservative alternatives.
- The executor re-plans only at safe materialization boundaries and logs
  reproducible decisions.
"""

from .relations import Relation, hash_join, partitioned_join, named_multiset
from .stats import Interval, ColumnStat, RelationStat, Catalog
from .query import Query, JoinEdge, INNER, LEFT
from .costmodel import CostParams, estimate_plan
from .planner import make_plan, make_plan_from_stats, legal_orders, heuristic_order
from .executor import Executor, Status, ExecutionResult, Decision

__all__ = [
    "Relation", "hash_join", "partitioned_join", "named_multiset",
    "Interval", "ColumnStat", "RelationStat", "Catalog",
    "Query", "JoinEdge", "INNER", "LEFT",
    "CostParams", "estimate_plan",
    "make_plan", "make_plan_from_stats", "legal_orders", "heuristic_order",
    "Executor", "Status", "ExecutionResult", "Decision",
]
