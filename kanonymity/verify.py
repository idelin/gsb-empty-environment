"""小域精确枚举核验：暴力枚举全部层向量，确认优化结果的最优性。

仅适用于域很小（层向量总数有限）的情形；这是精确核验，不是统计推断。
"""
from __future__ import annotations

import itertools
import math

from .core import Record, evaluate
from .hierarchy import Hierarchy
from .optimize import SearchResult


def brute_force_best_loss(
    records: list[Record],
    hierarchies: tuple[Hierarchy, ...],
    k: int,
    budget: int,
    count_mode: str = "record",
    suppression_penalty: float = 1.0,
) -> float:
    """精确枚举所有层向量，返回最低可行损失；无可行解返回 inf。"""
    best = math.inf
    for vec in itertools.product(*(range(h.max_level + 1) for h in hierarchies)):
        ev = evaluate(records, vec, hierarchies, k, count_mode,
                      suppression_penalty)
        if ev.feasible(budget) and ev.total_loss < best:
            best = ev.total_loss
    return best


def check_optimal(
    result: SearchResult,
    records: list[Record],
    hierarchies: tuple[Hierarchy, ...],
    k: int,
    budget: int,
    count_mode: str = "record",
    suppression_penalty: float = 1.0,
    tol: float = 1e-9,
) -> bool:
    """核验 SearchResult 的最优性声明是否与精确枚举一致。"""
    exact = brute_force_best_loss(records, hierarchies, k, budget, count_mode,
                                  suppression_penalty)
    if not result.optimal:
        # 中断结果只核验界的一致性：lower <= exact <= upper
        return result.lower_bound <= exact + tol and exact <= result.upper_bound + tol
    if result.best is None:
        return math.isinf(exact)
    return abs(result.best.loss - exact) <= tol
