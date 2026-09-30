"""k-匿名求解器：等价类、不足 k 记录、信息损失、精确枚举与上下界。

定义（详见 README）：
- 等价类：在给定泛化向量下，所有准标识符泛化值相同的记录集合。
- 类大小：类内“不同主体数”（同一主体多条记录只计一次）。
- 不足 k：类大小 < k 的类；其全部记录为“不足 k 记录”。
- 抑制：删除记录。给定泛化向量下，最小抑制集 = 所有不足 k 类的全部记录
  （抑制只会缩小类，无法通过部分抑制使不足 k 的类达标）。
- 信息损失（总量）：
    L = Σ_{保留记录 r} record_loss(r) + sup_cost * (抑制记录数)
  record_loss 支持 "ncp" 与 "lm" 两种声明指标（见 README）。
- 下界：任何向量 v 的真实损失 L(v) >= 不抑制时的泛化损失 LB(v)
  （因 sup_cost=1 >= 任何单记录权重），用于分支定界与预算中断报告。
"""
from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from itertools import product

from .dataset import Record
from .hierarchy import Hierarchy

METRICS = ("ncp", "lm")


def group_records(records, hierarchies, vector):
    """按泛化向量重新分组。每次调用都从原始记录重新计算，不沿用旧计数。"""
    classes: dict[tuple, list[Record]] = {}
    for r in records:
        key = tuple(
            h.generalize(v, lv) for h, v, lv in zip(hierarchies, r.values, vector)
        )
        classes.setdefault(key, []).append(r)
    return classes


def subject_count(recs) -> int:
    return len({r.subject for r in recs})


def record_loss(hierarchies, values, vector, metric: str) -> float:
    if metric == "ncp":
        return sum(
            h.weight(v, lv) for h, v, lv in zip(hierarchies, values, vector)
        ) / len(hierarchies)
    if metric == "lm":
        return sum(
            (lv / h.max_level) if h.max_level else 0.0
            for h, lv in zip(hierarchies, vector)
        ) / len(hierarchies)
    raise ValueError(f"未知指标: {metric!r}，可选 {METRICS}")


@dataclass
class Evaluation:
    vector: tuple
    feasible: bool
    n_suppressed: int
    suppressed: tuple  # 被抑制记录的 rid
    loss: float | None  # 可行时为总损失，否则 None
    lower_bound: float  # 不抑制时的泛化损失（L(v) 的下界）
    classes: dict = field(default_factory=dict)


def evaluate(records, hierarchies, vector, k, sup_budget,
             metric="ncp", sup_cost=1.0) -> Evaluation:
    classes = group_records(records, hierarchies, vector)
    suppressed = tuple(
        r.rid for recs in classes.values() if subject_count(recs) < k for r in recs
    )
    feasible = len(suppressed) <= sup_budget
    lower_bound = sum(
        record_loss(hierarchies, r.values, vector, metric) for r in records
    )
    loss = None
    if feasible:
        supp = set(suppressed)
        loss = sum(
            record_loss(hierarchies, r.values, vector, metric)
            for r in records
            if r.rid not in supp
        ) + sup_cost * len(suppressed)
    return Evaluation(
        vector=tuple(vector),
        feasible=feasible,
        n_suppressed=len(suppressed),
        suppressed=suppressed,
        loss=loss,
        lower_bound=lower_bound,
        classes=classes,
    )


@dataclass
class Result:
    status: str  # "optimal" | "budget_exceeded" | "infeasible"
    best: Evaluation | None  # 已找到的最优可行方案（上界来源）
    lower_bound: float | None  # 最优损失的下界
    upper_bound: float | None  # 最优损失的上界（= best.loss）
    n_evaluated: int
    n_total: int


def solve_exact(records, hierarchies, k, sup_budget, metric="ncp",
                sup_cost=1.0, max_evaluations=None) -> Result:
    """对泛化向量网格做最佳优先精确枚举。

    - 小域上完整枚举即得全局最优（status == "optimal"）。
    - max_evaluations 限制评估次数；超限时返回 budget_exceeded，
      并给出最优损失的上下界 [lower_bound, upper_bound]。
    """
    heap = []
    for vector in product(*[range(h.max_level + 1) for h in hierarchies]):
        lb = sum(
            record_loss(hierarchies, r.values, vector, metric) for r in records
        )
        heapq.heappush(heap, (lb, vector))
    n_total = len(heap)

    best: Evaluation | None = None
    n_evaluated = 0
    stopped_by_budget = False
    while heap:
        if best is not None and heap[0][0] >= best.loss:
            break  # 剩余向量下界不优于当前最优，可判定最优
        if max_evaluations is not None and n_evaluated >= max_evaluations:
            stopped_by_budget = True
            break
        _, vector = heapq.heappop(heap)
        ev = evaluate(records, hierarchies, vector, k, sup_budget, metric, sup_cost)
        n_evaluated += 1
        if ev.feasible and (best is None or ev.loss < best.loss):
            best = ev

    if stopped_by_budget:
        status = "budget_exceeded"
        lower = heap[0][0] if heap else (best.loss if best else None)
    elif best is None:
        status = "infeasible"
        lower = None
    else:
        status = "optimal"
        lower = best.loss
    return Result(
        status=status,
        best=best,
        lower_bound=lower,
        upper_bound=best.loss if best else None,
        n_evaluated=n_evaluated,
        n_total=n_total,
    )


def build_evidence(records, evaluation: Evaluation, k) -> dict:
    """每记录归属证据：所属等价类、类主体计数、是否满足 k、是否被抑制。"""
    supp = set(evaluation.suppressed)
    evidence = {}
    for key, recs in evaluation.classes.items():
        sc = subject_count(recs)
        for r in recs:
            evidence[r.rid] = {
                "rid": r.rid,
                "subject": r.subject,
                "class_key": key,
                "class_subject_count": sc,
                "class_record_count": len(recs),
                "meets_k": sc >= k,
                "suppressed": r.rid in supp,
            }
    return evidence


def below_k_records(records, hierarchies, vector, k) -> list:
    """返回给定泛化向量下所有不足 k 的记录（按主体计数）。"""
    classes = group_records(records, hierarchies, vector)
    return [
        r for recs in classes.values() if subject_count(recs) < k for r in recs
    ]
