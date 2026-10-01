"""budget_coverage: 带预算的最大覆盖（含依赖、互斥、最低覆盖次数）。

纯标准库实现：贪心启发式、全子集枚举精确参考、可恢复分支定界。
所有数值以 int / Fraction 精确表示，拒绝 float，不调用任何求解器。
"""
from .model import Problem, SetSpec, Status, optimality_gap, to_rational
from .greedy import GreedyResult, greedy_cover
from .exact import ExactResult, exact_solve
from .branch_bound import BranchBoundSolver, PruneRecord, SearchResult
from .witness import (
    ValidationReport,
    Witness,
    make_witness,
    validate_witness,
)

__all__ = [
    "Problem",
    "SetSpec",
    "Status",
    "to_rational",
    "optimality_gap",
    "greedy_cover",
    "GreedyResult",
    "exact_solve",
    "ExactResult",
    "BranchBoundSolver",
    "SearchResult",
    "PruneRecord",
    "Witness",
    "ValidationReport",
    "make_witness",
    "validate_witness",
]
