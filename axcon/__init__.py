"""Explicit axis-label contraction planner and chunked sparse executor."""
from .cache import ChunkIndexCache, SubexpressionCache, build_join_index
from .dense import dense_contract
from .errors import ValidationError
from .executor import (Budget, BudgetExceeded, ExecutionInterrupted,
                      ExecutionResult, Executor)
from .planner import (AxisMapping, OperandRef, Plan, PlanStep, build_plan,
                      collect_sizes, enumerate_plans)
from .tensor import SparseTensor

__all__ = [
    "AxisMapping", "Budget", "BudgetExceeded", "ChunkIndexCache",
    "ExecutionInterrupted", "ExecutionResult", "Executor", "OperandRef",
    "Plan", "PlanStep", "SparseTensor", "SubexpressionCache",
    "ValidationError", "build_join_index", "build_plan", "collect_sizes",
    "dense_contract", "enumerate_plans",
]
