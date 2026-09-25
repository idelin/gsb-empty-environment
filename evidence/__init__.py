"""多版本时态证据选择：版本可见性 + 业务区间 + 来源互斥约束下的极大可行时间段。"""
from .model import EvidenceStore, Event, FactVersion, MutexRuleVersion
from .solver import (
    FEASIBLE,
    INFEASIBLE,
    SOLVER_VERSION,
    UNKNOWN,
    SolveResult,
    solve,
    solve_store,
)
from .incremental import IncrementalSolver, UpdateReport
from .replay import replay

__all__ = [
    "EvidenceStore",
    "Event",
    "FactVersion",
    "MutexRuleVersion",
    "SolveResult",
    "solve",
    "solve_store",
    "IncrementalSolver",
    "UpdateReport",
    "replay",
    "FEASIBLE",
    "INFEASIBLE",
    "UNKNOWN",
    "SOLVER_VERSION",
]
