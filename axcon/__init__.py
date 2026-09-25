"""axcon: explicit axis-label contraction planner (pure standard library)."""

from .api import PlanResult, PlanStats, contract_many
from .cache import ContractionCache
from .dense import dense_contract
from .engine import Budget, StepStats, contract_pair, extract_diagonals
from .errors import AxconError, BudgetExceeded, CheckpointConflict, ValidationError
from .planner import Operand, Plan, StepSpec, plan_order
from .tensor import Delta, SparseTensor

__all__ = [
    "AxconError", "Budget", "BudgetExceeded", "CheckpointConflict",
    "ContractionCache", "Delta", "Operand", "Plan", "PlanResult", "PlanStats",
    "SparseTensor", "StepSpec", "StepStats", "ValidationError",
    "contract_many", "contract_pair", "dense_contract", "extract_diagonals",
    "plan_order",
]
