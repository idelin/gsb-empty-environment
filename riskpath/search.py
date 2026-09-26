"""带成本/时延双预算与共享风险组状态的标签设置搜索（可恢复）。"""
from __future__ import annotations

import heapq
import itertools
import math
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .model import Edge, Graph, Number


class Status(Enum):
    OPTIMAL = "optimal"        # 搜索耗尽且找到可行解：预算内最优已证明
    INFEASIBLE = "infeasible"  # 搜索耗尽但预算内无任何可行路径（无解，已证明）
    UNDECIDED = "undecided"    # 搜索被扩展上限截断：预算内可行/最优未决，可恢复


@dataclass(frozen=True)
class Label:
    """部分路径标签。

    groups  记录已经过的不同风险组（成功率只依赖该集合）；
    visited 保证简单路径（不重复经过节点）。
    """

    node: str
    cost: Number
    delay: Number
    groups: frozenset
    visited: frozenset
    nll: float
    edges: Tuple[int, ...]

    def signature(self):
        return (self.cost, self.delay, self.groups)


def _covers(a: Label, b: Label) -> bool:
    """a 不劣于 b（允许相等）。"""
    return (
        a.cost <= b.cost
        and a.delay <= b.delay
        and a.groups <= b.groups
        and a.visited <= b.visited
    )


def dominates(a: Label, b: Label) -> bool:
    """a 严格支配 b（搜索剪枝用，要求同节点）。

    正确性：每个风险组 nll >= 0，故 groups 子集蕴含 nll 不更差；
    visited 子集保证 b 的任何简单延拓对 a 同样可行，且新增风险组
    仍是子集，因此 a 的任意延拓都不劣于 b 的对应延拓。
    """
    if a.node != b.node:
        return False
    if not _covers(a, b):
        return False
    return (a.cost, a.delay, a.groups, a.visited) != (b.cost, b.delay, b.groups, b.visited)


def dominates_at_target(a, b) -> bool:
    """终点前沿支配：不再延拓，无需 visited 条件。对 Label/PathInfo 通用。"""
    if not (a.cost <= b.cost and a.delay <= b.delay and a.groups <= b.groups):
        return False
    return (a.cost, a.delay, a.groups) != (b.cost, b.delay, b.groups)


def pareto_frontier(labels) -> list:
    """非支配标签集合（先按 (cost, delay, groups) 去重再过滤）。"""
    unique: Dict[tuple, object] = {}
    for label in labels:
        unique.setdefault((label.cost, label.delay, label.groups), label)
    items = list(unique.values())
    return [x for x in items if not any(dominates_at_target(o, x) for o in items)]


class SearchState:
    """可恢复搜索状态：优先堆 + 每节点非支配标签列表。

    run() 因扩展上限返回 UNDECIDED 后，可再次调用 run() 无缝继续。
    """

    def __init__(self, graph: Graph, source: str, target: str, max_cost, max_delay) -> None:
        self.graph = graph
        self.source = source
        self.target = target
        self.max_cost = to_num(max_cost)
        self.max_delay = to_num(max_delay)
        self._counter = itertools.count()
        self._heap: List[Tuple[float, Number, int, Label]] = []
        self._labels: Dict[str, List[Label]] = {n: [] for n in graph.nodes}
        self.expansions = 0
        self.generated = 0
        self.pruned_budget = 0
        self.pruned_dominance = 0
        self.pruned_zero_prob = 0
        self.exhausted = False
        self._push(Label(node=source, cost=0, delay=0,
                         groups=frozenset(), visited=frozenset({source}),
                         nll=0.0, edges=()))

    # -- 内部 ---------------------------------------------------------
    def _push(self, label: Label) -> None:
        heapq.heappush(self._heap, (label.nll, label.cost, next(self._counter), label))
        self._labels[label.node].append(label)
        self.generated += 1

    def _alive(self, label: Label) -> bool:
        return any(x is label for x in self._labels[label.node])

    def _extend(self, label: Label) -> None:
        for edge_id, edge in self.graph.outgoing(label.node):
            if edge.dst in label.visited:
                continue  # 简单路径约束
            cost = label.cost + edge.cost
            delay = label.delay + edge.delay
            if cost > self.max_cost or delay > self.max_delay:
                self.pruned_budget += 1
                continue
            if edge.group in label.groups:
                groups, nll = label.groups, label.nll  # 同组不重复计费
            else:
                groups = label.groups | {edge.group}
                nll = label.nll + self.graph.groups[edge.group].nll
            if math.isinf(nll):
                self.pruned_zero_prob += 1
                continue  # 成功概率为 0，nll 只增不减，永不可能最优
            new = Label(node=edge.dst, cost=cost, delay=delay, groups=groups,
                        visited=label.visited | {edge.dst}, nll=nll,
                        edges=label.edges + (edge_id,))
            existing = self._labels[edge.dst]
            if any(_covers(e, new) for e in existing):
                self.pruned_dominance += 1
                continue
            self._labels[edge.dst] = [e for e in existing if not dominates(new, e)]
            self._push(new)

    # -- 对外 ---------------------------------------------------------
    def run(self, max_expansions: Optional[int] = None) -> Status:
        """运行至堆耗尽或达到扩展上限；UNDECIDED 后可再次调用继续。"""
        while self._heap:
            if max_expansions is not None and self.expansions >= max_expansions:
                return Status.UNDECIDED
            _, _, _, label = heapq.heappop(self._heap)
            if not self._alive(label):
                continue  # 入堆后被更新的标签支配
            self.expansions += 1
            if label.node == self.target:
                continue  # 终点标签不再延拓
            self._extend(label)
        self.exhausted = True
        return Status.OPTIMAL if self.feasible_labels() else Status.INFEASIBLE

    def feasible_labels(self) -> List[Label]:
        """终点处满足双预算的全部存活标签（构造时即保证预算内）。"""
        return list(self._labels[self.target])

    def frontier(self) -> List[Label]:
        return pareto_frontier(self.feasible_labels())

    def best(self) -> Optional[Label]:
        frontier = self.frontier()
        if not frontier:
            return None
        return min(frontier, key=lambda x: (x.nll, x.cost, x.delay))

    def result(self, status: Status) -> "SearchResult":
        return SearchResult(status=status, frontier=self.frontier(),
                            best=self.best(), state=self)


def to_num(value) -> Number:
    from .model import to_number
    return to_number(value)


@dataclass
class SearchResult:
    status: Status
    frontier: List[Label]
    best: Optional[Label]
    state: SearchState

    def success_of(self, label: Label) -> Fraction:
        return self.state.graph.success_of(label.groups)

    @property
    def best_success(self) -> Optional[Fraction]:
        return None if self.best is None else self.success_of(self.best)

    def path_edges(self, label: Label) -> List[Edge]:
        return [self.state.graph.edges[i] for i in label.edges]

    def path_nodes(self, label: Label) -> List[str]:
        nodes = [self.state.source]
        nodes.extend(e.dst for e in self.path_edges(label))
        return nodes


def solve(graph: Graph, source: str, target: str, max_cost, max_delay,
          max_expansions: Optional[int] = None) -> SearchResult:
    """一次性求解入口。需要可恢复语义时请直接使用 SearchState。"""
    state = SearchState(graph, source, target, max_cost, max_delay)
    status = state.run(max_expansions=max_expansions)
    return state.result(status)
