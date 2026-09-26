"""单机调度分支定界求解器。

问题：1 | r_j, seq-dep setup(类型切换) | sum T_j
- 求总迟延 sum max(0, C_j - d_j) 最小的任务次序；
- 支持搜索预算（节点数 / 秒数），超预算返回最佳可行解且标记为“预算未决”；
- 下界 = 已发生迟延 + 剩余任务迟延下界（位置松弛与单任务松弛取大）；
- 占优规则：已排集合与末尾类型相同的状态下，(时间更早, 已发生迟延更小) 占优。
"""
from __future__ import annotations

import time
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple

from .model import Job, Schedule, SetupMatrix, build_schedule


class Status(Enum):
    """求解结论。可行 / 最优 / 预算未决 三者明确区分。"""

    OPTIMAL = "optimal"      # 搜索空间耗尽，已证明最优
    FEASIBLE = "feasible"    # 预算未决：预算耗尽，返回最佳可行解，不保证最优


@dataclass(frozen=True)
class SearchBudget:
    """搜索预算：最大展开节点数和/或最大秒数；None 表示不限。"""

    max_nodes: Optional[int] = None
    max_seconds: Optional[float] = None


@dataclass
class SolveResult:
    status: Status
    schedule: Optional[Schedule]      # 最佳可行排程
    objective: Optional[Fraction]     # 该排程的总迟延（上界）
    lower_bound: Optional[Fraction]   # 已证明的全局下界
    nodes_explored: int
    budget_exhausted: bool

    @property
    def proven_optimal(self) -> bool:
        return self.status is Status.OPTIMAL

    @property
    def gap(self) -> Optional[Fraction]:
        if self.objective is None or self.lower_bound is None:
            return None
        return self.objective - self.lower_bound


class BranchAndBoundSolver:
    def __init__(
        self,
        jobs: Sequence[Job],
        setups: Optional[SetupMatrix] = None,
        budget: Optional[SearchBudget] = None,
    ):
        self._jobs: List[Job] = list(jobs)
        ids = [j.job_id for j in self._jobs]
        if len(set(ids)) != len(ids):
            raise ValueError("job_id 重复")
        self._setups = setups if setups is not None else SetupMatrix()
        self._budget = budget if budget is not None else SearchBudget()
        self._n = len(self._jobs)
        self._release = [j.release for j in self._jobs]
        self._proc = [j.processing for j in self._jobs]
        self._due = [j.due for j in self._jobs]
        self._type = [j.job_type for j in self._jobs]

    # ---- 内部：模拟与下界 ----

    def _remaining_lb(self, mask: int, current_time: Fraction) -> Fraction:
        """剩余任务（未在 mask 中）总迟延的下界。"""
        remaining = [j for j in range(self._n) if not (mask >> j) & 1]
        if not remaining:
            return Fraction(0)
        release, proc, due = self._release, self._proc, self._due

        # 位置松弛：忽略切换耗时，机器从 t0 起不间断加工；
        # 第 k 小完工位置与第 k 小截止时间配对（凸函数重排不等式，下界有效）。
        t0 = current_time
        min_rel = min(release[j] for j in remaining)
        if min_rel > t0:
            t0 = min_rel
        ps = sorted(proc[j] for j in remaining)
        ds = sorted(due[j] for j in remaining)
        t = t0
        positional = Fraction(0)
        for p, d in zip(ps, ds):
            t += p
            if t > d:
                positional += t - d

        # 单任务松弛：每个任务独立地最早完工。
        individual = Fraction(0)
        for j in remaining:
            c = current_time if current_time > release[j] else release[j]
            c += proc[j]
            if c > due[j]:
                individual += c - due[j]

        return positional if positional > individual else individual

    def _greedy_order(self) -> Tuple[int, ...]:
        """贪心构造初始可行解：每步选局部迟延最小者（作为初始上界，不计预算）。"""
        remaining = set(range(self._n))
        t = Fraction(0)
        last = -1
        order: List[int] = []
        while remaining:
            best_j, best_key, best_completion = -1, None, None
            for j in remaining:
                setup = Fraction(0) if last < 0 else self._setups.between(self._type[last], self._type[j])
                start = t + setup
                if start < self._release[j]:
                    start = self._release[j]
                completion = start + self._proc[j]
                key = (completion - self._due[j], self._due[j], j)
                if best_key is None or key < best_key:
                    best_key, best_j, best_completion = key, j, completion
            order.append(best_j)
            remaining.discard(best_j)
            t = best_completion
            last = best_j
        return tuple(order)

    def _evaluate(self, order: Sequence[int]) -> Fraction:
        t = Fraction(0)
        last = -1
        total = Fraction(0)
        for j in order:
            setup = Fraction(0) if last < 0 else self._setups.between(self._type[last], self._type[j])
            start = t + setup
            if start < self._release[j]:
                start = self._release[j]
            completion = start + self._proc[j]
            late = completion - self._due[j]
            if late > 0:
                total += late
            t = completion
            last = j
        return total

    # ---- 主流程 ----

    def solve(self) -> SolveResult:
        n = self._n
        if n == 0:
            return SolveResult(Status.OPTIMAL, Schedule(()), Fraction(0), Fraction(0), 0, False)

        best_order = self._greedy_order()
        best_obj = self._evaluate(best_order)

        full_mask = (1 << n) - 1
        # 栈元素: (order, mask, time, last_idx, incurred_tardiness, total_lower_bound)
        root_lb = self._remaining_lb(0, Fraction(0))
        stack: List[tuple] = [((), 0, Fraction(0), -1, Fraction(0), root_lb)]
        memo = {}  # (mask, last_idx) -> [(time, incurred)] 非占优状态集
        nodes = 0
        exhausted = False
        clock_start = time.monotonic()
        max_nodes = self._budget.max_nodes
        max_seconds = self._budget.max_seconds

        while stack:
            if max_nodes is not None and nodes >= max_nodes:
                exhausted = True
                break
            if max_seconds is not None and time.monotonic() - clock_start >= max_seconds:
                exhausted = True
                break

            order, mask, t, last, incurred, lb = stack.pop()
            nodes += 1
            if lb >= best_obj:
                continue  # 下界剪枝
            key = (mask, last)
            states = memo.setdefault(key, [])
            if any(t0 <= t and i0 <= incurred for t0, i0 in states):
                continue  # 占优剪枝
            states[:] = [(t0, i0) for t0, i0 in states if not (t <= t0 and incurred <= i0)]
            states.append((t, incurred))

            if mask == full_mask:
                if incurred < best_obj:
                    best_obj = incurred
                    best_order = order
                continue

            children = []
            for j in range(n):
                if (mask >> j) & 1:
                    continue
                setup = Fraction(0) if last < 0 else self._setups.between(self._type[last], self._type[j])
                start = t + setup
                if start < self._release[j]:
                    start = self._release[j]
                completion = start + self._proc[j]
                late = completion - self._due[j]
                new_incurred = incurred + (late if late > 0 else Fraction(0))
                child_lb = new_incurred + self._remaining_lb(mask | (1 << j), completion)
                if child_lb >= best_obj:
                    continue  # 入栈前剪枝（不影响全局下界：其 lb 不差于当前上界）
                children.append((order + (j,), mask | (1 << j), completion, j, new_incurred, child_lb))
            # 深度优先：下界小的后入栈、先弹出
            children.sort(key=lambda c: (c[5], c[0][-1]), reverse=True)
            stack.extend(children)

        if exhausted or stack:
            status = Status.FEASIBLE  # 预算未决：不贴最优标签
            open_lbs = [entry[5] for entry in stack]
            global_lb = min([best_obj] + open_lbs)
        else:
            status = Status.OPTIMAL
            global_lb = best_obj

        schedule = build_schedule([self._jobs[j] for j in best_order], self._setups)
        return SolveResult(
            status=status,
            schedule=schedule,
            objective=best_obj,
            lower_bound=global_lb,
            nodes_explored=nodes,
            budget_exhausted=exhausted or bool(stack),
        )
