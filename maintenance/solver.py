"""精确分支定界求解器（不调用任何外部求解器）。

状态三分：
- OPTIMAL:           搜索树在预算内耗尽，现任最优已被证明最优。
- FEASIBLE:          预算耗尽，但已找到可行解（最优性未决）。
- BUDGET_UNDECIDED:  预算耗尽且连一个完整节点都未评估，无可报告解。

预算以“扩展节点数”计量（max_nodes=None 表示不限）。
所有收益/成本用 fractions.Fraction 精确运算。
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Iterator, Optional

from .model import Instance, Job, validate


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"


@dataclass(frozen=True)
class Assignment:
    """一个被选中作业的落位：pieces[k] 为分配到班次 k 的槽数。"""

    job_id: str
    pieces: tuple[int, ...]

    @property
    def shifts_used(self) -> int:
        return sum(1 for x in self.pieces if x > 0)


@dataclass(frozen=True)
class Result:
    status: Status
    value: Optional[Fraction]          # BUDGET_UNDECIDED 时为 None
    selections: tuple[Assignment, ...]  # BUDGET_UNDECIDED 时为空
    nodes_expanded: int
    max_nodes: Optional[int]

    @property
    def proven_optimal(self) -> bool:
        return self.status is Status.OPTIMAL


class _BudgetExhausted(Exception):
    pass


def _splits(duration: int, caps: tuple[int, ...]) -> Iterator[tuple[int, ...]]:
    """枚举把 duration 个槽分配到各班次的所有方案，x_k <= caps[k]。"""
    if not caps:
        if duration == 0:
            yield ()
        return
    head, rest = caps[0], caps[1:]
    rest_sum = sum(rest)
    lo = max(0, duration - rest_sum)
    hi = min(head, duration)
    for x in range(hi, lo - 1, -1):
        for tail in _splits(duration - x, rest):
            yield (x,) + tail


def solve(instance: Instance, max_nodes: Optional[int] = None) -> Result:
    """分支定界求最大净收益：sum(score) - sum(startup * 占用班次数)。"""
    validate(instance)
    if max_nodes is not None and max_nodes < 0:
        raise ValueError("max_nodes 不能为负")

    lengths = tuple(s.length for s in instance.shifts)
    groups = tuple(sorted({j.group for j in instance.jobs} | {g.name for g in instance.groups}))
    gidx = {g: i for i, g in enumerate(groups)}
    limits = {g.name: g.limit for g in instance.groups}

    # 作业按单位净收益上界降序排列，尽早拿到好现任解；id 兜底保证确定性。
    jobs = sorted(instance.jobs, key=lambda j: (j.startup - j.score, j.job_id))
    n = len(jobs)

    # 上界：当前值 + 后续每个作业 max(0, score - startup)（至少占一个班次）。
    suffix = [Fraction(0)] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix[i] = suffix[i + 1] + max(Fraction(0), jobs[i].score - jobs[i].startup)

    cap0 = tuple(
        tuple(limits[g] * lengths[k] for k in range(len(lengths))) for g in groups
    )

    best: list[Optional[Fraction]] = [None]
    best_sel: list[tuple[Assignment, ...]] = [()]
    nodes = [0]

    def search(idx: int, remaining: tuple[tuple[int, ...], ...],
               value: Fraction, sel: tuple[Assignment, ...]) -> None:
        if max_nodes is not None and nodes[0] >= max_nodes:
            raise _BudgetExhausted
        nodes[0] += 1
        if best[0] is None or value > best[0]:
            best[0] = value
            best_sel[0] = sel
        if idx == n:
            return
        if value + suffix[idx] <= best[0]:
            return
        job = jobs[idx]
        g = gidx[job.group]
        caps = tuple(min(remaining[g][k], lengths[k]) for k in range(len(lengths)))
        for pieces in _splits(job.duration, caps):
            used = sum(1 for x in pieces if x > 0)
            new_rem = list(remaining)
            new_rem[g] = tuple(remaining[g][k] - pieces[k] for k in range(len(lengths)))
            gain = job.score - used * job.startup
            search(idx + 1, tuple(new_rem), value + gain,
                   sel + (Assignment(job.job_id, pieces),))
        search(idx + 1, remaining, value, sel)

    try:
        search(0, cap0, Fraction(0), ())
        status = Status.OPTIMAL
    except _BudgetExhausted:
        status = Status.FEASIBLE if best[0] is not None else Status.BUDGET_UNDECIDED

    if best[0] is None:
        return Result(status, None, (), nodes[0], max_nodes)
    ordered = tuple(sorted(best_sel[0], key=lambda a: a.job_id))
    return Result(status, best[0], ordered, nodes[0], max_nodes)


def brute_force(instance: Instance) -> Fraction:
    """独立参考实现：枚举子集 + 组内 DP 求最小启动成本，用于交叉验证。"""
    validate(instance)
    lengths = tuple(s.length for s in instance.shifts)
    limits = {g.name: g.limit for g in instance.groups}
    by_group: dict[str, list[Job]] = {}
    for job in instance.jobs:
        by_group.setdefault(job.group, []).append(job)

    def min_startup(jobs: list[Job], limit: int) -> Optional[Fraction]:
        dp: dict[tuple[int, ...], Fraction] = {
            tuple(limit * l for l in lengths): Fraction(0)
        }
        for job in jobs:
            nxt: dict[tuple[int, ...], Fraction] = {}
            for state, cost in dp.items():
                caps = tuple(min(state[k], lengths[k]) for k in range(len(lengths)))
                for pieces in _splits(job.duration, caps):
                    used = sum(1 for x in pieces if x > 0)
                    ns = tuple(state[k] - pieces[k] for k in range(len(lengths)))
                    nc = cost + used * job.startup
                    if ns not in nxt or nc < nxt[ns]:
                        nxt[ns] = nc
            if not nxt:
                return None
            dp = nxt
        return min(dp.values())

    best = Fraction(0)  # 空集恒可行
    ids = list(range(len(instance.jobs)))
    for r in range(len(ids) + 1):
        for combo in itertools.combinations(ids, r):
            sub: dict[str, list[Job]] = {}
            for i in combo:
                sub.setdefault(instance.jobs[i].group, []).append(instance.jobs[i])
            total = Fraction(0)
            ok = True
            for g, gjobs in sub.items():
                ms = min_startup(gjobs, limits[g])
                if ms is None:
                    ok = False
                    break
                total += sum(j.score for j in gjobs) - ms
            if ok and total > best:
                best = total
    return best
