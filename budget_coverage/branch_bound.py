"""可恢复的分支定界求解器。

- 显式栈迭代（非递归），随时可用 snapshot()/restore() 中断与恢复；
- 每个节点计算可靠上界：分数背包松弛（重叠收益重复计算只会高估，
  且忽略依赖/互斥/最低覆盖约束，放松只会抬高上界）；
- 每次剪枝都记录 PruneRecord，含具体数值的界限理由；
- 状态严格区分 OPTIMAL / FEASIBLE / INFEASIBLE / UNDECIDED（预算未决）。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import NamedTuple

from .model import Problem, Status


@dataclass
class PruneRecord:
    """一次剪枝的记录：决策路径、原因代码、含数值的界限理由。"""

    path: tuple                 # 各层决策（1=选, 0=弃），按内部顺序
    reason: str                 # budget / mutex / dependency / min_coverage / bound
    detail: str                 # 含具体数值的理由
    bound: Fraction | None      # 该子树上界（硬约束剪枝时为 None）
    incumbent: Fraction | None  # 剪枝时的当前最优值

    def to_dict(self):
        return {
            "path": list(self.path),
            "reason": self.reason,
            "detail": self.detail,
            "bound": str(self.bound) if self.bound is not None else None,
            "incumbent": str(self.incumbent) if self.incumbent is not None else None,
        }

    @classmethod
    def from_dict(cls, d):
        return cls(
            path=tuple(d["path"]),
            reason=d["reason"],
            detail=d["detail"],
            bound=Fraction(d["bound"]) if d["bound"] is not None else None,
            incumbent=Fraction(d["incumbent"]) if d["incumbent"] is not None else None,
        )


@dataclass
class SearchResult:
    status: Status
    selected: tuple | None      # 选中的集合 id（无可行解时为 None）
    profit: Fraction | None     # 当前最优可行收益（下界）
    upper_bound: Fraction | None  # 可靠上界
    lower_bound: Fraction | None
    nodes_explored: int
    prunes: list                # list[PruneRecord]
    message: str
    problem_version: int


class _Info(NamedTuple):
    kind: str                   # "prune" | "leaf" | "expand"
    reason: str | None
    detail: str | None
    bound: Fraction | None
    profit: Fraction | None     # 仅 leaf 时有意义


class BranchBoundSolver:
    """对集合做 选/弃 二叉搜索，包含分支优先、深度优先。"""

    def __init__(self, problem: Problem):
        self.problem = problem
        self.order = self._default_order()
        self.stack: list[tuple] = [()]
        self.incumbent: tuple | None = None          # 原始下标，已排序
        self.incumbent_profit: Fraction | None = None
        self.nodes_explored = 0
        self.prunes: list[PruneRecord] = []

    # ---------- 内部 ----------

    def _default_order(self):
        p = self.problem

        def key(i):
            s = p.sets[i]
            gain = sum((p.weights[e] for e in s.elements), Fraction(0))
            if s.cost == 0:
                return (1, gain)  # 零成本集合排在最前
            return (0, gain / s.cost)

        return sorted(range(len(p.sets)), key=key, reverse=True)

    def _decode(self, path):
        included, excluded = set(), set()
        for pos, d in enumerate(path):
            (included if d else excluded).add(self.order[pos])
        undecided = [self.order[pos] for pos in range(len(path), len(self.order))]
        return included, excluded, undecided

    def _fractional_bound(self, undecided, counts, remaining_budget) -> Fraction:
        """分数背包松弛上界。

        每个未定集合的边际收益按"当前未覆盖元素"计算，集合间重叠被重复
        计入（只会高估）；零成本集合的收益全额计入，不做任何除法。
        """
        p = self.problem
        total = Fraction(0)
        budget_left = remaining_budget
        positive = []
        for i in undecided:
            s = p.sets[i]
            gain = sum((p.weights[e] for e in s.elements if counts[e] == 0),
                       Fraction(0))
            if gain <= 0:
                continue
            if s.cost == 0:
                total += gain
            else:
                positive.append((gain, s.cost))
        positive.sort(key=lambda gc: gc[0] / gc[1], reverse=True)
        for gain, cost in positive:
            if budget_left <= 0:
                break
            if cost <= budget_left:
                total += gain
                budget_left -= cost
            else:
                total += gain * budget_left / cost
                budget_left = Fraction(0)
                break
        return total

    def _analyze(self, path) -> _Info:
        p = self.problem
        included, excluded, undecided = self._decode(path)
        cost = p.total_cost(included)
        if cost > p.budget:
            return _Info("prune", "budget",
                         f"已选成本 {cost} 超过预算 {p.budget}，子树内成本只会更大",
                         None, None)
        for g in p.mutex_groups:
            if len(included & g) > 1:
                names = sorted(p.sets[i].sid for i in included & g)
                return _Info("prune", "mutex", f"互斥组冲突: {names}", None, None)
        for i in included:
            for j in p.deps.get(i, ()):
                if j in excluded:
                    return _Info("prune", "dependency",
                                 f"集合 {p.sets[i].sid} 依赖 {p.sets[j].sid}，"
                                 "后者已被排除", None, None)
        counts = p.coverage_counts(included)
        for e, req in p.min_coverage.items():
            possible = sum(1 for i in undecided if e in p.sets[i].elements)
            if counts[e] + possible < req:
                return _Info("prune", "min_coverage",
                             f"元素 {e} 需至少 {req} 次覆盖：当前 {counts[e]} 次，"
                             f"剩余集合至多再提供 {possible} 次", None, None)
        profit = p.profit(included)
        bound = profit + self._fractional_bound(undecided, counts, p.budget - cost)
        if self.incumbent_profit is not None and bound <= self.incumbent_profit:
            return _Info("prune", "bound",
                         f"子树上界 {bound} = 已计收益 {profit} + 松弛上界 "
                         f"{bound - profit}，不超过当前最优 {self.incumbent_profit}",
                         bound, None)
        if not undecided:
            return _Info("leaf", None, None, bound, profit)
        return _Info("expand", None, None, bound, None)

    # ---------- 搜索 ----------

    def search(self, max_nodes: int | None = None) -> SearchResult:
        """最多再探索 max_nodes 个节点；None 表示不限。可反复调用以继续。"""
        remaining = max_nodes
        while self.stack and (remaining is None or remaining > 0):
            path = self.stack.pop()
            self.nodes_explored += 1
            if remaining is not None:
                remaining -= 1
            info = self._analyze(path)
            if info.kind == "prune":
                self.prunes.append(PruneRecord(
                    path, info.reason, info.detail, info.bound,
                    self.incumbent_profit))
            elif info.kind == "leaf":
                if self.incumbent_profit is None or info.profit > self.incumbent_profit:
                    self.incumbent_profit = info.profit
                    self.incumbent = tuple(sorted(self._decode(path)[0]))
            else:
                self.stack.append(path + (0,))  # 排除分支后压栈
                self.stack.append(path + (1,))  # 包含分支先被弹出
        return self._result()

    def _stack_bounds(self):
        bounds = []
        for path in self.stack:
            b = self._analyze(path).bound
            if b is not None:
                bounds.append(b)
        return bounds

    def _result(self) -> SearchResult:
        p = self.problem
        ids = None
        if self.incumbent is not None:
            ids = tuple(p.sets[i].sid for i in self.incumbent)
        if not self.stack:
            if self.incumbent is None:
                return SearchResult(Status.INFEASIBLE, None, None, None, None,
                                    self.nodes_explored, list(self.prunes),
                                    "搜索完成：没有任何可行解", p.version)
            return SearchResult(Status.OPTIMAL, ids, self.incumbent_profit,
                                self.incumbent_profit, self.incumbent_profit,
                                self.nodes_explored, list(self.prunes),
                                f"搜索完成：已证明最优，收益 {self.incumbent_profit}",
                                p.version)
        bounds = self._stack_bounds()
        if self.incumbent is not None and (not bounds or max(bounds) <= self.incumbent_profit):
            return SearchResult(Status.OPTIMAL, ids, self.incumbent_profit,
                                self.incumbent_profit, self.incumbent_profit,
                                self.nodes_explored, list(self.prunes),
                                "所有剩余子树的上界均不超过当前最优，提前判定最优",
                                p.version)
        ub = max(bounds) if bounds else None
        if self.incumbent is None:
            if ub is None:
                return SearchResult(Status.INFEASIBLE, None, None, None, None,
                                    self.nodes_explored, list(self.prunes),
                                    "剩余子树均不可行且未找到可行解：判定不可行",
                                    p.version)
            return SearchResult(Status.UNDECIDED, None, None, ub, None,
                                self.nodes_explored, list(self.prunes),
                                f"搜索预算耗尽：暂无可行解，剩余子树上界 {ub}，"
                                "最优性未决", p.version)
        ub = max(ub, self.incumbent_profit) if ub is not None else self.incumbent_profit
        gap = ub - self.incumbent_profit
        return SearchResult(Status.FEASIBLE, ids, self.incumbent_profit, ub,
                            self.incumbent_profit, self.nodes_explored,
                            list(self.prunes),
                            f"搜索预算耗尽：当前最优 {self.incumbent_profit}，"
                            f"可靠上界 {ub}，绝对差距 {gap}", p.version)

    # ---------- 快照 / 恢复 ----------

    def snapshot(self) -> dict:
        """可 JSON 序列化的搜索快照，用于中断后恢复。"""
        return {
            "problem_version": self.problem.version,
            "order": list(self.order),
            "stack": [list(path) for path in self.stack],
            "incumbent": list(self.incumbent) if self.incumbent is not None else None,
            "incumbent_profit": (str(self.incumbent_profit)
                                 if self.incumbent_profit is not None else None),
            "nodes_explored": self.nodes_explored,
            "prunes": [r.to_dict() for r in self.prunes],
        }

    @classmethod
    def restore(cls, problem: Problem, snap: dict) -> "BranchBoundSolver":
        if snap["problem_version"] != problem.version:
            raise ValueError(
                "问题在快照后已变更（如动态删集合），快照失效，需重新求解")
        solver = cls(problem)
        solver.order = list(snap["order"])
        solver.stack = [tuple(path) for path in snap["stack"]]
        solver.incumbent = (tuple(snap["incumbent"])
                            if snap["incumbent"] is not None else None)
        solver.incumbent_profit = (Fraction(snap["incumbent_profit"])
                                   if snap["incumbent_profit"] is not None else None)
        solver.nodes_explored = snap["nodes_explored"]
        solver.prunes = [PruneRecord.from_dict(d) for d in snap["prunes"]]
        return solver
