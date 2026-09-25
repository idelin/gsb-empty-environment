"""Multi-table join planning and adaptive execution (stdlib only)."""
from .cost import CostModel, JoinEstimate, estimate_join
from .executor import Executor, ExecutionReport
from .planner import JoinStep, Plan, PlanChoice, Query, plan
from .relations import INNER, LEFT_OUTER, Relation, execute_join
from .stats import Interval, RelStats, StatsProvider

__all__ = [
    "CostModel", "JoinEstimate", "estimate_join",
    "Executor", "ExecutionReport",
    "JoinStep", "Plan", "PlanChoice", "Query", "plan",
    "INNER", "LEFT_OUTER", "Relation", "execute_join",
    "Interval", "RelStats", "StatsProvider",
]
