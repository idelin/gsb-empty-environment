"""贪心启发式：零成本候选永远优先（不除零），再按性价比，最后修复最低覆盖。

只承诺"找到可行解或如实报告失败"，绝不声称最优。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Problem, Status


@dataclass
class GreedyResult:
    status: Status          # 只会是 FEASIBLE 或 UNDECIDED
    selected: tuple         # 选中的集合 id
    profit: Fraction | None
    cost: Fraction | None
    message: str
    problem_version: int


def greedy_cover(problem: Problem) -> GreedyResult:
    n = len(problem.sets)
    m = len(problem.weights)
    selected: set[int] = set()
    counts = [0] * m
    cost = Fraction(0)

    def apply(add):
        nonlocal cost
        for i in add:
            selected.add(i)
            cost += problem.sets[i].cost
            for e in problem.sets[i].elements:
                counts[e] += 1

    def candidate(i):
        """返回 (新增集合, 新增成本, 新增收益)；违反互斥或无需新增时返回 None。

        选 i 意味着同时选其依赖闭包，因此成本/收益都按闭包中尚未选中的部分计算。
        """
        add = problem.dep_closure({i}) - selected
        if not add:
            return None
        for g in problem.mutex_groups:
            if len((selected | add) & g) > 1:
                return None
        extra = sum((problem.sets[j].cost for j in add), Fraction(0))
        gain = sum(
            (problem.weights[e] for j in add for e in problem.sets[j].elements
             if counts[e] == 0),
            Fraction(0),
        )
        return add, extra, gain

    # 主循环：有效成本为 0 的候选（零成本集合及其零成本闭包）永远先处理，
    # 收益/成本 的比值只对正成本候选计算，避免除零。
    while True:
        best_free = None
        best_ratio = None
        best_ratio_cand = None
        for i in range(n):
            if i in selected:
                continue
            cand = candidate(i)
            if cand is None:
                continue
            add, extra, gain = cand
            if gain <= 0:
                continue  # 空集或元素均已被覆盖：不增加收益
            if cost + extra > problem.budget:
                continue
            if extra == 0:
                if best_free is None or gain > best_free[2]:
                    best_free = cand
            else:
                ratio = gain / extra
                if best_ratio is None or ratio > best_ratio:
                    best_ratio = ratio
                    best_ratio_cand = cand
        if best_free is not None:
            apply(best_free[0])
        elif best_ratio_cand is not None:
            apply(best_ratio_cand[0])
        else:
            break

    # 修复最低覆盖次数（收益仍只计一次，这里只补覆盖次数）
    while True:
        lacking = [e for e, req in problem.min_coverage.items() if counts[e] < req]
        if not lacking:
            break
        best = None  # (extra_cost, -progress, add)
        for i in range(n):
            if i in selected:
                continue
            cand = candidate(i)
            if cand is None:
                continue
            add, extra, _gain = cand
            if cost + extra > problem.budget:
                continue
            progress = sum(
                1 for e in lacking
                if any(e in problem.sets[j].elements for j in add)
            )
            if progress == 0:
                continue
            key = (extra, -progress)
            if best is None or key < best[0]:
                best = (key, add)
        if best is None:
            return GreedyResult(
                Status.UNDECIDED, (), None, None,
                f"启发式无法满足元素 {lacking} 的最低覆盖次数（不代表问题不可行）",
                problem.version,
            )
        apply(best[1])

    msgs = problem.violations(selected)
    if msgs:
        return GreedyResult(Status.UNDECIDED, (), None, None,
                            "启发式内部错误: " + "; ".join(msgs), problem.version)
    profit = problem.profit(selected)
    ids = tuple(sorted(problem.sets[i].sid for i in selected))
    return GreedyResult(Status.FEASIBLE, ids, profit, cost,
                        f"贪心得到可行解，收益 {profit}（不声称最优）",
                        problem.version)
