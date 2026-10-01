"""在“每字段一个层级”的组合格子上搜索满足 k 约束的最小损失解。

- exact_search：穷举全部层级组合，小规模精确求解。
- beam_search：从全原始层 (0,...,0) 出发按损失升序扩展，frontier 宽度与
  评估预算受限；不同扩展路径收敛到同一状态时用 visited 集合去重。

状态语义（“找不到”与“未搜完”严格分开）：
- OPTIMAL：已搜完全空间，返回全局最优可行解。
- INFEASIBLE：已搜完全空间，不存在可行解。
- FEASIBLE_TRUNCATED：预算/宽度截断，找到可行解但未搜完，不保证最优。
- NO_SOLUTION_TRUNCATED：预算/宽度截断，未找到可行解且未搜完（不等于不存在）。
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from enum import Enum
from math import prod

from .loss import combination_loss


class Status(Enum):
    OPTIMAL = "OPTIMAL"
    INFEASIBLE = "INFEASIBLE"
    FEASIBLE_TRUNCATED = "FEASIBLE_TRUNCATED"
    NO_SOLUTION_TRUNCATED = "NO_SOLUTION_TRUNCATED"


@dataclass
class SearchResult:
    status: Status
    levels: tuple | None
    loss: float | None
    explored: int
    space_size: int


def equivalence_classes(records, hierarchies, levels):
    """按泛化后的准标识符元组分组，返回 {泛化键: 类大小}。"""
    groups = {}
    for rec in records:
        key = tuple(
            hier.generalize(rec[i], levels[i]) for i, hier in enumerate(hierarchies)
        )
        groups[key] = groups.get(key, 0) + 1
    return groups


def satisfies_k(groups, k):
    return all(size >= k for size in groups.values())


def exact_search(records, hierarchies, k):
    space = prod(h.max_level + 1 for h in hierarchies)
    best_levels = None
    best_loss = None
    explored = 0
    for levels in itertools.product(*(range(h.max_level + 1) for h in hierarchies)):
        explored += 1
        groups = equivalence_classes(records, hierarchies, levels)
        if not satisfies_k(groups, k):
            continue
        loss = combination_loss(records, hierarchies, levels)
        if best_loss is None or loss < best_loss:
            best_loss = loss
            best_levels = levels
    status = Status.OPTIMAL if best_levels is not None else Status.INFEASIBLE
    return SearchResult(status, best_levels, best_loss, explored, space)


def beam_search(records, hierarchies, k, width=4, budget=10_000):
    if width < 1:
        raise ValueError("width 必须 >= 1")
    if budget < 1:
        raise ValueError("budget 必须 >= 1")
    dims = len(hierarchies)
    space = prod(h.max_level + 1 for h in hierarchies)
    start = (0,) * dims
    loss_cache = {start: combination_loss(records, hierarchies, start)}
    visited = {start}
    frontier = [start]
    explored = 0
    best_levels = None
    best_loss = None
    while frontier and explored < budget:
        survivors = []
        for levels in frontier:
            if explored >= budget:
                break
            explored += 1
            groups = equivalence_classes(records, hierarchies, levels)
            if satisfies_k(groups, k):
                loss = loss_cache[levels]
                if best_loss is None or loss < best_loss:
                    best_loss = loss
                    best_levels = levels
            else:
                survivors.append(levels)
        # 单调性剪枝：损失随层级不减，幸存状态的下界都不优于当前最优即可停止
        if best_loss is not None and all(
            loss_cache[s] >= best_loss for s in survivors
        ):
            break
        # 扩展：每个幸存状态提升一个字段；不同路径收敛到同一状态时经 visited 去重
        candidates = {}
        for levels in survivors:
            for i in range(dims):
                if levels[i] < hierarchies[i].max_level:
                    nxt = list(levels)
                    nxt[i] += 1
                    nxt = tuple(nxt)
                    if nxt not in visited:
                        visited.add(nxt)
                        loss_cache[nxt] = combination_loss(records, hierarchies, nxt)
                        candidates[nxt] = loss_cache[nxt]
        # 预算截断：只保留损失最小的 width 个候选
        frontier = sorted(candidates, key=lambda lv: (candidates[lv], lv))[:width]
    if best_levels is not None:
        status = Status.OPTIMAL if explored >= space else Status.FEASIBLE_TRUNCATED
    else:
        status = Status.INFEASIBLE if explored >= space else Status.NO_SOLUTION_TRUNCATED
    return SearchResult(status, best_levels, best_loss, explored, space)
