"""小实例精确参考：全子集枚举。用于核验分支定界与贪心。"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Problem, Status


@dataclass
class ExactResult:
    status: Status          # OPTIMAL 或 INFEASIBLE
    selected: tuple | None  # 选中的集合 id
    profit: Fraction | None
    upper_bound: Fraction | None
    nodes_explored: int
    problem_version: int


def exact_solve(problem: Problem, max_sets: int = 20) -> ExactResult:
    """枚举全部 2^n 个子集，逐一检查可行性并取收益最大者。"""
    n = len(problem.sets)
    if n > max_sets:
        raise ValueError(f"集合数 {n} 超过枚举上限 {max_sets}，请改用分支定界")
    best_profit: Fraction | None = None
    best_sel: tuple | None = None
    nodes = 0
    for mask in range(1 << n):
        nodes += 1
        selection = [i for i in range(n) if mask >> i & 1]
        if problem.violations(selection):
            continue
        p = problem.profit(selection)
        if best_profit is None or p > best_profit:
            best_profit = p
            best_sel = tuple(selection)
    if best_sel is None:
        return ExactResult(Status.INFEASIBLE, None, None, None, nodes,
                           problem.version)
    ids = tuple(problem.sets[i].sid for i in best_sel)
    return ExactResult(Status.OPTIMAL, ids, best_profit, best_profit, nodes,
                       problem.version)
